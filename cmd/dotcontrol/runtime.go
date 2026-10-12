package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/activity"
	"github.com/dotcontrol/dotcontrol/internal/codexbridge"
	"github.com/dotcontrol/dotcontrol/internal/config"
	"github.com/dotcontrol/dotcontrol/internal/desktop"
	"github.com/dotcontrol/dotcontrol/internal/driver"
	"github.com/dotcontrol/dotcontrol/internal/engine"
	"github.com/dotcontrol/dotcontrol/internal/gateway"
	"github.com/dotcontrol/dotcontrol/internal/localapi"
	"github.com/dotcontrol/dotcontrol/internal/mcpserver"
	"github.com/dotcontrol/dotcontrol/internal/model"
	"github.com/dotcontrol/dotcontrol/internal/onboard"
	"github.com/dotcontrol/dotcontrol/internal/service"
	"github.com/dotcontrol/dotcontrol/internal/tunnel"
	"github.com/dotcontrol/dotcontrol/internal/vault"
)

type connectionProof struct {
	DevicesSeen    bool `json:"devices_seen"`
	ImageSeen      bool `json:"image_seen"`
	ImageConfirmed bool `json:"image_confirmed"`
}
type runtimeOwner struct {
	mu                       sync.Mutex
	ctx                      context.Context
	stop                     context.CancelFunc
	dir                      string
	cfg                      config.Config
	vault                    vault.Vault
	eng                      *engine.Engine
	bridge                   *codexbridge.Bridge
	monitor                  *activity.Managed
	native                   *driver.Cua
	sup                      *tunnel.Supervisor
	tunnelError, driverError string
	driverDiagnostic         map[string]any
	permissions              permissionStatus
	proof                    connectionProof
	revoked, noTunnel        bool
	repair                   chan struct{}
}
type observedBackend struct {
	owner *runtimeOwner
	local service.Backend
}

func (b observedBackend) Call(ctx context.Context, method string, r service.Request) (any, error) {
	v, err := b.local.Call(ctx, method, r)
	if err == nil {
		b.owner.mu.Lock()
		switch method {
		case "devices":
			b.owner.proof.DevicesSeen = true
		case "observe":
			if obs, ok := v.(model.Observation); ok && len(obs.View.Images) > 0 {
				b.owner.proof.ImageSeen = true
			}
		}
		b.owner.mu.Unlock()
	}
	return v, err
}
func serve(ctx context.Context, dir string, c config.Config, v vault.Vault, args []string) error {
	if err := desktop.OwnChildren(); err != nil {
		return err
	}
	if c.GatewayListen != "" {
		return serveLegacy(ctx, dir, c, v, args)
	}
	for _, a := range args {
		if a == "--gateway" {
			return serveLegacy(ctx, dir, c, v, args)
		}
	}
	f := flags("serve")
	noTunnel := f.Bool("no-tunnel", false, "Local diagnostics only")
	if err := f.Parse(args); err != nil {
		return err
	}
	listener, err := localapi.Listen(c.Listen)
	if err != nil {
		return errors.New("local_port_in_use")
	}
	defer listener.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	monitor := activity.NewManaged(ctx, c.ActivityHelper)
	defer monitor.Close()
	eng, err := engine.New(engine.Config{DeviceID: c.DeviceID, Name: c.Name, StateDir: dir, InitialPaused: true, IdleThreshold: time.Duration(c.IdleSeconds) * time.Second}, unavailableDriver{}, monitor)
	if err != nil {
		return err
	}
	defer eng.Close()
	bridge, err := codexbridge.New(codexbridge.Config{DeviceID: c.DeviceID, StateDir: dir, Runtime: &codexbridge.RuntimeConfig{ConnectionsDir: filepath.Join(dir, "connections"), Engine: eng, Acceptance: delegationAcceptance(), OpenThread: openOwnedCodexThread}})
	if err != nil {
		return err
	}
	defer bridge.Close()
	_, revErr := os.Stat(filepath.Join(dir, "revoked"))
	if revErr != nil && !os.IsNotExist(revErr) {
		return errors.New("revocation_state_unavailable")
	}
	owner := &runtimeOwner{ctx: ctx, stop: cancel, dir: dir, cfg: c, vault: v, eng: eng, monitor: monitor, sup: &tunnel.Supervisor{}, revoked: revErr == nil, noTunnel: *noTunnel, repair: make(chan struct{}, 1)}
	if owner.revoked {
		_ = eng.Pause(true)
	}
	adminToken, err := v.Get("admin-token")
	if err != nil {
		return errors.New("local_credential_missing")
	}
	mcpToken, err := v.Get("mcp-token")
	if err != nil {
		return errors.New("local_credential_missing")
	}
	ui := onboard.New(c.Listen, owner)
	desktop.Tray(ctx, func(action string) {
		if action == "onboard" {
			_ = desktop.OpenURL(ui.LaunchURL())
		} else if action == "quit" {
			cancel()
		} else {
			_, _ = owner.Action(ctx, action)
		}
	})
	startMacSessionComponents(ctx, dir)
	owner.bridge = bridge
	backend := service.Local{Engine: eng, Bridge: bridge, BaseStatus: owner.baseStatus}
	guard := func(h http.Handler) http.Handler {
		return localapi.Guard(mcpToken, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			owner.mu.Lock()
			revoked := owner.revoked
			owner.mu.Unlock()
			if revoked {
				http.Error(w, "access_revoked", 403)
				return
			}
			h.ServeHTTP(w, r)
		}))
	}
	mux := http.NewServeMux()
	mux.Handle("/onboard/", ui)
	mux.Handle("/mcp", guard(mcpserver.HTTP(observedBackend{owner, backend})))
	// The existing authenticated RPC remains separate from browser sessions.
	mux.Handle("/rpc", guard(gateway.Handler(backend)))
	mux.Handle("/admin/", localapi.Guard(adminToken, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "method_rejected", 405)
			return
		}
		var out any
		var e error
		switch r.URL.Path {
		case "/admin/status":
			out, e = owner.Status(r.Context())
		case "/admin/onboard":
			out = map[string]string{"url": ui.LaunchURL()}
		case "/admin/shutdown":
			out = map[string]bool{"stopping": true}
			defer cancel()
		case "/admin/repair":
			out, e = owner.Action(r.Context(), "repair")
		case "/admin/pause", "/admin/resume", "/admin/revoke":
			out, e = owner.Action(r.Context(), strings.TrimPrefix(r.URL.Path, "/admin/"))
		default:
			http.NotFound(w, r)
			return
		}
		if e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})))
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16384}
	server.BaseContext = func(net.Listener) context.Context { return ctx }
	defer server.Close()
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); owner.maintain() }()
	defer func() {
		cancel()
		// Stop admissions and active native work before waiting for maintenance.
		// Close preserves the durable pause choice; quitting is not a resume.
		_ = server.Close()
		// Official turns must finish their bounded interruption/reconciliation
		// before the shared engine is closed. Unconfirmed stops remain journaled.
		bridge.Close()
		_ = eng.Close()
		<-workerDone
		owner.mu.Lock()
		s := owner.sup
		owner.mu.Unlock()
		s.Stop()
	}()
	fmt.Fprintln(os.Stderr, "DotControl local agent ready; remote and image verification are separate.")
	select {
	case <-ctx.Done():
		return nil
	case err = <-done:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

var tunnelIDPattern = regexp.MustCompile(`^tunnel_[A-Za-z0-9_-]{4,180}$`)

func (o *runtimeOwner) Connect(ctx context.Context, in onboard.Connect) error {
	if !tunnelIDPattern.MatchString(in.TunnelID) {
		return errors.New("invalid_tunnel_id")
	}
	in.Name = strings.TrimSpace(in.Name)
	if len(in.Name) < 1 || len(in.Name) > 240 || strings.ContainsAny(in.Name, "\r\n\x00") {
		return errors.New("invalid_device_name")
	}
	if in.RuntimeKey != "" && (len(in.RuntimeKey) < 20 || len(in.RuntimeKey) > 4096 || strings.ContainsAny(in.RuntimeKey, "\r\n\x00")) {
		return errors.New("invalid_runtime_key")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if in.RuntimeKey != "" {
		if o.vault.Set("tunnel-key", in.RuntimeKey) != nil {
			return errors.New("credential_store_failed")
		}
	}
	if key, e := o.vault.Get("tunnel-key"); e != nil || key == "" {
		return errors.New("tunnel_runtime_key_missing")
	}
	next := o.cfg
	next.Name = in.Name
	next.TunnelID = in.TunnelID
	if config.Save(o.dir, next) != nil {
		return errors.New("config_save_failed")
	}
	if o.revoked {
		if e := os.Remove(filepath.Join(o.dir, "revoked")); e != nil && !os.IsNotExist(e) {
			return errors.New("revocation_state_unavailable")
		}
	}
	o.cfg = next
	o.eng.Rename(next.Name)
	o.revoked = false
	o.proof = connectionProof{}
	o.eng.Invalidate()
	o.requestRepair()
	return nil
}
func (o *runtimeOwner) requestRepair() {
	select {
	case o.repair <- struct{}{}:
	default:
	}
}
func (o *runtimeOwner) Action(ctx context.Context, action string) (any, error) {
	switch action {
	case "check_update":
		return availableRelease(ctx)
	case "repair":
		o.requestRepair()
	case "pause", "resume":
		o.mu.Lock()
		defer o.mu.Unlock()
		revoked := o.revoked
		connected := o.native != nil && o.native.Diagnostic()["connected"] == true
		if action == "resume" {
			if revoked {
				return nil, errors.New("access_revoked")
			}
			d := o.eng.Device()
			if !connected || !d.Activity.Available || d.Activity.Locked {
				return nil, errors.New("local_checks_not_ready")
			}
			if !o.permissions.ready() {
				return nil, errors.New("permissions_not_ready")
			}
		}
		if e := o.eng.Pause(action == "pause"); e != nil {
			return nil, e
		}
	case "revoke":
		o.mu.Lock()
		defer o.mu.Unlock()
		if e := o.eng.Pause(true); e != nil {
			return nil, e
		}
		if os.WriteFile(filepath.Join(o.dir, "revoked"), []byte("revoked\n"), 0600) != nil {
			return nil, errors.New("revocation_save_failed")
		}
		o.revoked = true
		o.sup.Stop()
		_ = o.vault.Delete("tunnel-key")
		o.proof = connectionProof{}
	case "confirm_image":
		o.mu.Lock()
		defer o.mu.Unlock()
		if !o.proof.ImageSeen {
			return nil, errors.New("image_not_observed")
		}
		o.proof.ImageConfirmed = true
	case "permissions", "permissions_screen", "permissions_activity":
		if runtime.GOOS == "darwin" {
			setting := "Privacy_Accessibility"
			if action == "permissions_screen" {
				setting = "Privacy_ScreenCapture"
			}
			if action == "permissions_activity" {
				setting = "Privacy_ListenEvent"
			}
			_ = desktop.OpenURL("x-apple.systempreferences:com.apple.preference.security?" + setting)
			o.mu.Lock()
			c := o.cfg
			o.mu.Unlock()
			if action == "permissions_activity" && c.ActivityHelper != "" {
				go func() {
					cmd := exec.CommandContext(o.ctx, c.ActivityHelper, "--request-permission")
					cmd.Stdout = nil
					cmd.Stderr = nil
					_ = cmd.Run()
				}()
			} else if action == "permissions" {
				if err := driver.ValidateExecutable(ctx, c.DriverPath); err != nil {
					return nil, err
				}
				go func() {
					cmd := exec.CommandContext(o.ctx, c.DriverPath, "permissions", "grant")
					cmd.Stdout = nil
					cmd.Stderr = nil
					_ = cmd.Run()
				}()
			}
			return map[string]string{"next": "Grant CuaDriver Accessibility and Screen Recording, DotControlActivity Input Monitoring; then repair."}, nil
		}
		return map[string]string{"next": "Keep the target app running in your signed-in desktop session."}, nil
	default:
		return nil, errors.New("unknown_action")
	}
	return map[string]bool{"ok": true}, nil
}
func (o *runtimeOwner) Status(ctx context.Context) (any, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	ds := o.driverStatus()
	ts := o.sup.Status()
	if o.tunnelError != "" {
		ts.Error = o.tunnelError
	}
	ts.Configured = o.cfg.TunnelID != ""
	d := o.eng.Device()
	d.Name = o.cfg.Name
	caps := o.bridge.Capabilities()
	d.Capabilities = caps
	d.CodexProfiles = o.bridge.ProfileCatalog()
	return map[string]any{"device": d, "driver_connected": ds["connected"], "driver_status": ds, "driver_error": o.driverError, "permissions": o.permissions, "tunnel": ts, "tunnel_id": o.cfg.TunnelID, "revoked": o.revoked, "plugin": o.proof, "autostart": desktop.AutostartEnabled(), "codex": map[string]any{"available": caps.CodexDelegation.Available, "capabilities": caps, "reason": "任务配置、连接状态和平台验收分别检查；完整跨电脑委派尚未通过验收。"}}, nil
}
func (o *runtimeOwner) Diagnostics(ctx context.Context) (any, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	ds := o.driverStatus()
	d := o.eng.Device()
	return map[string]any{"schema": 1, "version": model.Version, "os": runtime.GOOS, "arch": runtime.GOARCH, "time": time.Now().UTC(), "paused": d.Paused, "revoked": o.revoked, "activity_available": d.Activity.Available, "locked": d.Activity.Locked, "driver": ds, "driver_error": o.driverError, "permissions": o.permissions, "tunnel": o.sup.Status(), "tunnel_error": o.tunnelError, "plugin": o.proof, "codex_capabilities": o.bridge.Capabilities()}, nil
}
func (o *runtimeOwner) maintain() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	force := true
	first := true
	driverRepairPending := false
	for {
		if !first && (force || !o.monitor.Snapshot().Available) {
			o.monitor.Restart()
			o.eng.Invalidate()
		}
		first = false
		o.mu.Lock()
		c := o.cfg
		native := o.native
		sup := o.sup
		needDriver := native == nil || native.Diagnostic()["connected"] != true
		o.mu.Unlock()
		if force || needDriver || driverRepairPending {
			ctx, cancel := context.WithTimeout(o.ctx, 12*time.Second)
			reconnectErr := o.eng.Reconnect(ctx, func() model.Driver {
				d, err := driver.New(ctx, c.DriverPath)
				var diagnostic map[string]any
				if err == nil {
					diagnostic, err = probeDriver(ctx, d)
					if err != nil {
						d = nil
					}
				}
				o.mu.Lock()
				defer o.mu.Unlock()
				o.native = d
				o.driverDiagnostic = diagnostic
				o.driverError = ""
				if err != nil {
					o.driverError = safeDriverError(err)
					return unavailableDriver{}
				}
				return d
			})
			driverRepairPending = reconnectErr != nil
			cancel()
		}
		if force {
			sup.Stop()
			sup = &tunnel.Supervisor{}
			o.mu.Lock()
			o.sup = sup
			o.mu.Unlock()
		}
		permissions := checkPermissions(o.ctx, c.DriverPath)
		o.mu.Lock()
		o.permissions = permissions
		o.mu.Unlock()
		// Serialize credential reads and publishing the supervisor with Connect
		// and revoke. A stale snapshot must never start a previously revoked ID.
		o.mu.Lock()
		if !o.revoked && !o.noTunnel && o.cfg.TunnelID != "" && !o.sup.Status().Configured {
			next, err := tunnel.Start(o.ctx, o.dir, o.cfg, o.vault)
			o.tunnelError = ""
			if err != nil {
				o.tunnelError = safeTunnelError(err)
			} else {
				o.sup = next
			}
		}
		o.mu.Unlock()
		force = false
		select {
		case <-o.ctx.Done():
			return
		case <-o.repair:
			force = true
		case <-ticker.C:
		}
	}
}
func (o *runtimeOwner) baseStatus() model.Capability {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.native != nil && o.native.Diagnostic()["connected"] == true {
		if !o.permissions.ready() {
			return model.Capability{State: "unavailable", Code: "permissions_" + o.permissions.State}
		}
		return model.Capability{Available: true, State: "connected"}
	}
	return model.Capability{State: "unavailable", Code: o.driverError}
}
func safeDriverError(err error) string {
	switch err.Error() {
	case "signature_invalid", "driver_install_location_invalid", "driver_version_unavailable", "driver_version_mismatch", "cua_driver_not_configured", "cua_driver_connection_failed", "driver_discovery_failed", "driver_apps_probe_failed", "driver_apps_probe_timeout", "app_discovery_failed", "window_discovery_failed", "driver_transport_failed", "driver_empty_response":
		return err.Error()
	}
	return "driver_unavailable"
}
func safeTunnelError(err error) string {
	switch err.Error() {
	case "tunnel_binary_missing", "tunnel_runtime_key_missing", "local_credential_missing", "tunnel_already_running", "tunnel_start_failed":
		return err.Error()
	}
	return "tunnel_unavailable"
}
