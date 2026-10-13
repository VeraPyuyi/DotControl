// DotControl runs in the signed-in user's desktop session.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
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
	"github.com/dotcontrol/dotcontrol/internal/service"
	"github.com/dotcontrol/dotcontrol/internal/tunnel"
	"github.com/dotcontrol/dotcontrol/internal/vault"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/term"
)

func main() {
	if handled, err := desktop.RunChildGuardian(os.Args[1:]); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, "DotControl:", err)
			os.Exit(1)
		}
		return
	}
	if e := run(os.Args[1:]); e != nil {
		fmt.Fprintln(os.Stderr, "DotControl:", e)
		os.Exit(1)
	}
}
func output(v any)                    { enc := json.NewEncoder(os.Stdout); enc.SetIndent("", "  "); _ = enc.Encode(v) }
func flags(name string) *flag.FlagSet { return flag.NewFlagSet(name, flag.ContinueOnError) }
func run(args []string) error {
	root := flags("dotcontrol")
	dir := root.String("state", config.DefaultDir(), "Private local configuration directory")
	if e := root.Parse(args); e != nil {
		return e
	}
	args = root.Args()
	if len(args) == 0 {
		usage()
		return nil
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if args[0] == "version" {
		fmt.Println(model.Version)
		return nil
	}
	if args[0] == "codex-probe" {
		f := flags("codex-probe")
		binary := f.String("binary", "codex", "Official local Codex binary")
		working := f.String("working-dir", "", "Probe directory; never reads existing threads")
		if e := f.Parse(args[1:]); e != nil {
			return e
		}
		if *working == "" {
			*working, _ = os.Getwd()
		}
		resolved, err := exec.LookPath(*binary)
		if err != nil {
			return errors.New("codex_binary_missing")
		}
		*binary, _ = filepath.Abs(resolved)
		*working, _ = filepath.Abs(*working)
		check, stop := context.WithTimeout(ctx, 30*time.Second)
		defer stop()
		report, err := codexbridge.Probe(check, codexbridge.ProbeConfig{Binary: *binary, WorkingDir: *working})
		output(report)
		return err
	}
	if args[0] == "setup" {
		return setup(*dir, args[1:])
	}
	if args[0] == "ssh-authorize" {
		return errors.New("feature_retired")
	}
	if args[0] == "experiment" {
		return experimentCLI(ctx, *dir, args[1:])
	}
	if args[0] == "terminal-check-helper" {
		return terminalCheckCLI(ctx, *dir, args[1:])
	}
	if args[0] == "terminal-check" {
		return terminalCheckCommand(ctx, *dir, args[1:])
	}
	if args[0] == "install" || args[0] == "uninstall" || (args[0] == "repair" && len(args) > 1) {
		return installation(ctx, *dir, args[0], args[1:])
	}
	c, e := config.Load(*dir)
	if e != nil {
		return errors.New("run 'dotcontrol setup' first")
	}
	v := vault.New(*dir)
	switch args[0] {
	case "quit":
		token, e := v.Get("admin-token")
		if e != nil {
			return errors.New("local_credential_missing")
		}
		_, e = (localapi.Client{Address: c.Listen, Token: token}).Post(ctx, "/admin/shutdown", map[string]any{})
		return e
	case "onboard":
		return launchOnboarding(ctx, *dir, c, v, args[1:])
	case "history":
		return historyCLI(ctx, *dir, args[1:])
	case "repair":
		return repairLocal(ctx, *dir, c, v)
	case "serve":
		return serve(ctx, *dir, c, v, args[1:])
	case "doctor":
		return doctor(ctx, *dir, c, v, args[1:])
	case "credential":
		return credential(v)
	case "status", "pause", "resume", "revoke":
		token, e := v.Get("admin-token")
		if e != nil {
			return errors.New("local_credential_missing")
		}
		client := localapi.Client{Address: c.Listen, Token: token}
		data, e := client.Post(ctx, "/admin/"+args[0], map[string]any{})
		if e != nil {
			return e
		}
		fmt.Println(string(data))
		return nil
	case "mcp":
		token, e := v.Get("mcp-token")
		if e != nil {
			return errors.New("local_credential_missing")
		}
		return mcpserver.New(localapi.Client{Address: c.Listen, Token: token}).Run(ctx, &mcp.StdioTransport{})
	case "pair":
		return pair(*dir, c, v, args[1:])
	case "probe":
		// Development diagnostic through the same authenticated API. This is NOT
		// official Dot acceptance, even when the six methods all work locally.
		if len(args) < 2 {
			return errors.New("probe requires devices/apps/observe/execute/task/cancel and JSON on stdin")
		}
		var req service.Request
		if args[1] != "devices" {
			dec := json.NewDecoder(io.LimitReader(os.Stdin, 1<<20))
			dec.DisallowUnknownFields()
			if e = dec.Decode(&req); e != nil {
				return errors.New("invalid_probe_input")
			}
		}
		token, e := v.Get("mcp-token")
		if e != nil {
			return e
		}
		value, e := (localapi.Client{Address: c.Listen, Token: token}).Call(ctx, args[1], req)
		if e != nil {
			return e
		}
		output(value)
		return nil
	default:
		usage()
		return errors.New("unknown_command")
	}
}
func usage() {
	fmt.Println(`DotControl — private desktop tools for official Dot

dotcontrol [--state DIRECTORY] COMMAND
  install     Install a verified prebuilt bundle; retains device data
  onboard     Open the protected local connection wizard
  repair      Reconnect local components without resuming desktop tasks
  uninstall   Remove owned program files; retain device data by default
  setup       Guided setup (or --name/--driver/--tunnel/--tunnel-id flags)
  credential  Store a tunnel runtime key in the operating-system vault
  doctor      Check local prerequisites; --online also runs official tunnel doctor
  serve       Run the device agent; --gateway serves paired devices instead
  status      Show local activity, connection readiness and pause state
  pause       Stop future input and interrupt the current task
  resume      Resume admission; fresh observations are still required
  revoke      Pause, stop the tunnel, and rotate its local access credential
  pair        init / add / list / revoke: pair devices for the optional gateway
  mcp         Stdio bridge for local MCP testing (not official Dot acceptance)
  probe       Local API diagnostics with JSON on stdin
  version`)
}
func setup(dir string, args []string) error {
	f := flags("setup")
	name := f.String("name", "", "Device display name")
	driverPath := f.String("driver", "cua-driver", "Pinned Cua Driver executable")
	tunnelPath := f.String("tunnel", "tunnel-client", "Official tunnel-client executable")
	tunnelID := f.String("tunnel-id", "", "Independent tunnel id for this device")
	listen := f.String("listen", "127.0.0.1:9317", "Local authenticated listener")
	helper := f.String("activity-helper", "", "macOS activity helper executable")
	idle := f.Int("idle", 60, "Seconds without human input required for foreground")
	peerListen := f.String("peer-listen", "", "Optional exact Tailscale IP:port for paired gateway access")
	if e := f.Parse(args); e != nil {
		return e
	}
	if _, e := os.Stat(filepath.Join(dir, "config.json")); e == nil {
		return errors.New("already_configured_edit_config_without_replacing_device_identity")
	}
	if *name == "" && term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Print("设备名称 / Device name: ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		*name = strings.TrimSpace(line)
	}
	if *idle < 1 || *idle > 86400 {
		return errors.New("idle_must_be_1_to_86400_seconds")
	}
	listener, e := localapi.Listen(*listen)
	if e != nil {
		return e
	}
	listener.Close()
	if *peerListen != "" {
		if e = gateway.ValidateListen(*peerListen); e != nil {
			return e
		}
	}
	c := config.New(*name, *driverPath)
	c.TunnelPath = *tunnelPath
	c.TunnelID = *tunnelID
	c.Listen = *listen
	c.ActivityHelper = *helper
	c.IdleSeconds = *idle
	c.GatewayListen = *peerListen
	if p, e := exec.LookPath(c.DriverPath); e == nil {
		c.DriverPath, _ = filepath.Abs(p)
	}
	if p, e := exec.LookPath(c.TunnelPath); e == nil {
		c.TunnelPath, _ = filepath.Abs(p)
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	v := vault.New(dir)
	for _, key := range []string{"admin-token", "mcp-token"} {
		if e = v.Set(key, config.Random()); e != nil {
			return errors.New("operating_system_vault_unavailable")
		}
	}
	if e = config.Save(dir, c); e != nil {
		return e
	}
	output(map[string]any{"device_id": c.DeviceID, "name": c.Name, "configured": true, "next": "dotcontrol doctor; dotcontrol credential; dotcontrol serve", "official_setup": "https://platform.openai.com/settings/organization/tunnels"})
	return nil
}
func credential(v vault.Vault) error {
	var key string
	if env := os.Getenv("DOTCONTROL_RUNTIME_KEY"); env != "" {
		key = env
	} else {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return errors.New("use_interactive_terminal_or_DOTCONTROL_RUNTIME_KEY_environment")
		}
		fmt.Fprint(os.Stderr, "Tunnel runtime key (hidden; Read + Use only): ")
		b, e := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if e != nil {
			return e
		}
		key = strings.TrimSpace(string(b))
		for i := range b {
			b[i] = 0
		}
	}
	if len(key) < 20 {
		return errors.New("invalid_runtime_key")
	}
	if e := v.Set("tunnel-key", key); e != nil {
		return errors.New("credential_store_failed")
	}
	fmt.Println("Runtime key stored in the operating-system vault.")
	return nil
}

type unavailableDriver struct{}

func (unavailableDriver) Apps(context.Context) (json.RawMessage, error) {
	return nil, errors.New("driver_unavailable")
}
func (unavailableDriver) Observe(context.Context, model.Target) (model.View, error) {
	return model.View{}, errors.New("driver_unavailable")
}
func (unavailableDriver) Check(context.Context, model.View) error {
	return errors.New("driver_unavailable")
}
func (unavailableDriver) SupportsBackground(model.Action, model.View) bool { return false }
func (unavailableDriver) Execute(context.Context, model.Action, model.View, string) model.Outcome {
	return model.Outcome{State: "needs_user"}
}
func (unavailableDriver) Release(context.Context) error { return nil }

func serveLegacy(ctx context.Context, dir string, c config.Config, v vault.Vault, args []string) error {
	f := flags("serve")
	gatewayMode := f.Bool("gateway", false, "Serve the explicitly paired device directory")
	withoutTunnel := f.Bool("no-tunnel", false, "Local development/diagnostics only")
	if e := f.Parse(args); e != nil {
		return e
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	listener, e := localapi.Listen(c.Listen)
	if e != nil {
		return e
	}
	defer listener.Close()
	monitor, monitorErr := activity.Start(ctx, c.ActivityHelper)
	defer monitor.Close()
	var backendDriver model.Driver = unavailableDriver{}
	var native *driver.Cua
	if !*gatewayMode {
		connectCtx, stop := context.WithTimeout(ctx, 15*time.Second)
		native, e = driver.New(connectCtx, c.DriverPath)
		stop()
		if e == nil {
			backendDriver = native
		}
	}
	eng, e := engine.New(engine.Config{DeviceID: c.DeviceID, Name: c.Name, StateDir: dir, IdleThreshold: time.Duration(c.IdleSeconds) * time.Second, ObservationTTL: 30 * time.Second}, backendDriver, monitor)
	if e != nil {
		return e
	}
	defer func() {
		// Keep the device writer lock while the native transport is terminated.
		_ = eng.Pause(true)
		if native != nil {
			_ = native.Close()
		}
		_ = eng.Close()
	}()
	pairing := &gateway.Pairing{Dir: dir, Vault: v}
	var backend service.Backend = service.Local{Engine: eng}
	var gatewaySwitch service.Switch
	gatewaySwitch.SetPaused(eng.Device().Paused)
	if *gatewayMode {
		identity, e := pairing.Identity(c.DeviceID)
		if e != nil {
			return e
		}
		gatewaySwitch.Backend = &gateway.Gateway{Pairing: pairing, Identity: identity}
		backend = &gatewaySwitch
	}
	mcpToken, e := v.Get("mcp-token")
	if e != nil {
		return errors.New("local_credential_missing")
	}
	adminToken, e := v.Get("admin-token")
	if e != nil {
		return errors.New("local_credential_missing")
	}
	var revoked atomic.Bool
	rpcGuard := func(h http.Handler) http.Handler {
		return localapi.Guard(mcpToken, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if revoked.Load() {
				http.Error(w, "access_revoked", 403)
				return
			}
			h.ServeHTTP(w, r)
		}))
	}
	mux := http.NewServeMux()
	mux.Handle("/mcp", rpcGuard(mcpserver.HTTP(backend)))
	mux.Handle("/rpc", rpcGuard(gateway.Handler(backend)))
	var supervision atomic.Pointer[tunnel.Supervisor]
	supervision.Store(&tunnel.Supervisor{})
	mux.Handle("/admin/", localapi.Guard(adminToken, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "POST required", 405)
			return
		}
		switch r.URL.Path {
		case "/admin/status":
			w.Header().Set("Content-Type", "application/json")
			driverStatus := map[string]any{"connected": false, "reconnection_required": false}
			if native != nil {
				driverStatus = native.Diagnostic()
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"device": eng.Device(), "tunnel": supervision.Load().Status(), "gateway": *gatewayMode, "gateway_paused": gatewaySwitch.Paused(), "revoked": revoked.Load(), "driver_connected": driverStatus["connected"], "driver_status": driverStatus, "activity_monitor_available": monitorErr == nil})
		case "/admin/pause", "/admin/resume", "/admin/revoke":
			paused := r.URL.Path != "/admin/resume"
			if revoked.Load() && !paused {
				http.Error(w, "restart_required_after_revoke", 409)
				return
			}
			if e := eng.Pause(paused); e != nil {
				http.Error(w, "pause_failed", 500)
				return
			}
			gatewaySwitch.SetPaused(paused)
			if r.URL.Path == "/admin/revoke" {
				revoked.Store(true)
				supervision.Load().Stop()
				if e := v.Set("mcp-token", config.Random()); e != nil {
					http.Error(w, "credential_rotation_failed_access_remains_revoked", 500)
					return
				}
				_ = v.Delete("tunnel-key")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"paused": paused, "revoked": revoked.Load()})
		default:
			http.NotFound(w, r)
		}
	})))
	httpServer := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16384}
	errorsCh := make(chan error, 2)
	go func() { errorsCh <- httpServer.Serve(listener) }()
	if c.GatewayListen != "" && !*gatewayMode {
		if e = gateway.ValidateListen(c.GatewayListen); e != nil {
			return e
		}
		identity, e := pairing.Identity(c.DeviceID)
		if e != nil {
			return e
		}
		remoteMux := http.NewServeMux()
		remoteMux.Handle("/rpc", pairing.Guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if revoked.Load() {
				http.Error(w, "access_revoked", 403)
				return
			}
			gateway.Handler(backend).ServeHTTP(w, r)
		})))
		peerServer := &http.Server{Addr: c.GatewayListen, Handler: remoteMux, TLSConfig: pairing.ServerTLS(identity), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 15 * time.Second, MaxHeaderBytes: 16384}
		go func() { errorsCh <- peerServer.ListenAndServeTLS("", "") }()
		defer peerServer.Close()
	}
	if !*withoutTunnel && c.TunnelID != "" {
		sup, startErr := tunnel.Start(ctx, dir, c, v)
		e = startErr
		if e != nil {
			httpServer.Close()
			return e
		}
		supervision.Store(sup)
		defer sup.Stop()
	}
	fmt.Fprintf(os.Stderr, "DotControl %s ready locally on %s; official Dot acceptance remains a separate check.\n", c.Name, c.Listen)
	select {
	case <-ctx.Done():
	case e = <-errorsCh:
		if !errors.Is(e, http.ErrServerClosed) {
			return e
		}
	}
	_ = eng.Pause(true)
	httpServer.Close()
	return nil
}
func doctor(ctx context.Context, dir string, c config.Config, v vault.Vault, args []string) error {
	f := flags("doctor")
	online := f.Bool("online", false, "Validate tunnel configuration and report live cloud polling separately")
	if e := f.Parse(args); e != nil {
		return e
	}
	checks := map[string]any{"os": runtime.GOOS, "arch": runtime.GOARCH, "supported_os": runtime.GOOS == "windows" || runtime.GOOS == "darwin", "device_id": c.DeviceID, "tunnel_id_configured": c.TunnelID != "", "official_dot_invocation": "not_verified", "mac_real_device": "not_verified"}
	version := func(path string) string {
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		b, e := exec.CommandContext(probeCtx, path, "--version").Output()
		if e != nil {
			return "missing_or_failed"
		}
		s := strings.TrimSpace(string(b))
		if len(s) > 150 {
			return "unexpected_version_output"
		}
		return s
	}
	if c.TerminalOnly {
		checks["driver_version"] = "not_required"
	} else if err := driver.ValidateExecutable(ctx, c.DriverPath); err != nil {
		checks["driver_version"] = err.Error()
	} else {
		checks["driver_version"] = version(c.DriverPath)
	}
	checks["tunnel_version"] = version(c.TunnelPath)
	key, keyErr := v.Get("tunnel-key")
	checks["runtime_key_in_os_vault"] = keyErr == nil && key != ""
	if c.TerminalOnly {
		checks["activity"] = "not_required"
		checks["terminal_only"] = true
	} else {
		monitor, e := activity.Start(ctx, c.ActivityHelper)
		if monitor != nil {
			defer monitor.Close()
			time.Sleep(100 * time.Millisecond)
			checks["activity"] = monitor.Snapshot()
		}
		checks["activity_initialization_ok"] = e == nil
	}
	token, e := v.Get("admin-token")
	if e == nil {
		data, e := (localapi.Client{Address: c.Listen, Token: token}).Post(ctx, "/admin/status", map[string]any{})
		if e == nil {
			checks["agent_status"] = json.RawMessage(data)
		} else {
			checks["agent_status"] = "not_running"
		}
	}
	if *online {
		if keyErr != nil || key == "" || c.TunnelID == "" {
			checks["official_tunnel_doctor"] = "blocked_missing_tunnel_id_or_runtime_key"
		} else {
			mcpToken, e := v.Get("mcp-token")
			if e != nil {
				return e
			}
			probeCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
			defer cancel()
			env, proxyArgs, networkErr := tunnel.ControlPlaneEnvironment(probeCtx, c.Network, key, mcpToken)
			if networkErr != nil {
				checks["official_tunnel_doctor"] = networkErr.Error()
			} else {
				args := []string{"doctor", "--control-plane.tunnel-id", c.TunnelID, "--control-plane.api-key", "env:DOTCONTROL_TUNNEL_KEY", "--mcp.server-url", "http://" + c.Listen + "/mcp", "--mcp.extra-headers", "X-DotControl-Token: env:DOTCONTROL_MCP_TOKEN"}
				cmd := exec.CommandContext(probeCtx, c.TunnelPath, append(args, proxyArgs...)...)
				cmd.Env = env
				cmd.Stdout = io.Discard
				cmd.Stderr = io.Discard
				if cmd.Run() == nil {
					checks["official_tunnel_doctor"] = "configuration_validated"
				} else {
					checks["official_tunnel_doctor"] = "configuration_check_failed"
				}
			}
		}
		checks["tunnel_cloud_connection"] = tunnel.Status{CloudState: "unavailable"}
		if token != "" {
			data, err := (localapi.Client{Address: c.Listen, Token: token}).Post(ctx, "/admin/status", map[string]any{})
			if err == nil {
				checks["tunnel_cloud_connection"] = doctorCloudStatus(data)
			}
		}
	}
	checks["setup_urls"] = []string{"https://platform.openai.com/settings/organization/tunnels", "https://chatgpt.com/plugins"}
	output(checks)
	return nil
}
func pair(dir string, c config.Config, v vault.Vault, args []string) error {
	if len(args) == 0 {
		return errors.New("pair requires init/add/list/revoke")
	}
	p := &gateway.Pairing{Dir: dir, Vault: v}
	switch args[0] {
	case "init":
		if _, e := p.Identity(c.DeviceID); e != nil {
			return e
		}
		b, e := os.ReadFile(filepath.Join(dir, "device.crt"))
		if e != nil {
			return e
		}
		cert, e := gateway.Certificate(b)
		if e != nil {
			return e
		}
		output(map[string]any{"device_id": c.DeviceID, "public_certificate": filepath.Join(dir, "device.crt"), "sha256": gateway.Fingerprint(cert)})
		return nil
	case "list":
		peers, e := p.Peers()
		if e != nil {
			return e
		}
		for i := range peers {
			peers[i].Certificate = ""
		}
		output(peers)
		return nil
	case "add":
		f := flags("pair add")
		certFile := f.String("cert", "", "Peer public certificate")
		address := f.String("url", "", "https://Tailscale-IP:port (device peers only)")
		role := f.String("role", "device", "device or controller")
		name := f.String("name", "", "Display name")
		if e := f.Parse(args[1:]); e != nil {
			return e
		}
		b, e := os.ReadFile(*certFile)
		if e != nil {
			return e
		}
		cert, e := gateway.Certificate(b)
		if e != nil {
			return e
		}
		if e = p.Add(gateway.Peer{DeviceID: cert.Subject.CommonName, Name: *name, URL: *address, Role: *role, Certificate: string(b)}); e != nil {
			return e
		}
		output(map[string]any{"paired": cert.Subject.CommonName, "sha256": gateway.Fingerprint(cert)})
		return nil
	case "revoke":
		if len(args) != 2 {
			return errors.New("pair revoke requires exact device_id")
		}
		return p.Revoke(args[1])
	default:
		return errors.New("unknown_pair_command")
	}
}
