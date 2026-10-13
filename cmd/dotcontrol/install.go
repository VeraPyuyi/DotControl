package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/config"
	"github.com/dotcontrol/dotcontrol/internal/desktop"
	"github.com/dotcontrol/dotcontrol/internal/install"
	"github.com/dotcontrol/dotcontrol/internal/localapi"
	"github.com/dotcontrol/dotcontrol/internal/vault"
	"github.com/gofrs/flock"
)

func agentName() string {
	if runtime.GOOS == "windows" {
		return "dotcontrol.exe"
	}
	return "dotcontrol"
}
func installation(ctx context.Context, dir, command string, args []string) error {
	f := flags(command)
	bundle := f.String("bundle", "", "Prebuilt payload directory; omit only to retry committed cleanup")
	root := f.String("program-root", desktop.ProgramRoot(), "Program files only")
	name := f.String("name", "DotControl "+runtime.GOOS, "Device name for first install")
	noStart := f.Bool("no-start", false, "Isolated test: no runtime, credentials, startup, or shared Mac components")
	legacy := f.String("legacy-executable", "", "Explicit absolute path to existing unmanaged DotControl binary")
	if e := f.Parse(args); e != nil {
		return e
	}
	if f.NArg() != 0 {
		return errors.New("unexpected_install_arguments")
	}
	if command == "uninstall" {
		err := install.Remove(ctx, install.RemoveOptions{Root: *root, StateDir: dir, BeforeRemove: func(ctx context.Context) error {
			if e := stopLocal(ctx, dir); e != nil {
				return e
			}
			return desktop.Autostart("", dir, false)
		}})
		if err != nil {
			return err
		}
		output(map[string]any{"uninstalled": true, "device_data_retained": true})
		return nil
	}
	c, e := config.Load(dir)
	if e != nil && !os.IsNotExist(e) {
		return errors.New("existing_configuration_invalid")
	}
	if e == nil && c.GatewayListen != "" {
		return errors.New("legacy_gateway_migration_not_supported")
	}
	if os.IsNotExist(e) {
		if instances := discoverInstances(dir); len(instances) > 0 {
			output(map[string]any{"state": "select_existing_instance", "instances": instances})
			return errors.New("existing_instance_selection_required")
		}
	}
	result, err := applyInstallation(ctx, installationRequest{State: dir, Root: *root, Bundle: *bundle, Name: *name, Legacy: *legacy, NoStart: *noStart}, defaultInstallHooks())
	if err == nil || errors.Is(err, install.ErrCleanupPending) {
		output(map[string]any{"installed": true, "version": result.Current.Version, "reused": result.Reused, "cleanup_pending": result.CleanupPending, "device_data_retained": true, "runtime_verified": !*noStart})
		return nil
	}
	return err
}
func validInstalledVersion(output []byte, targetVersion string) bool {
	return string(output) == targetVersion+"\n"
}
func stopLocal(ctx context.Context, dir string) error {
	c, e := config.Load(dir)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return errors.New("existing_configuration_invalid")
	}
	lock := flock.New(filepath.Join(dir, "engine.lock"))
	defer lock.Close()
	// This is a process-lifetime lock, not evidence of an active desktop task.
	if ok, e := lock.TryLock(); e != nil {
		return errors.New("existing_instance_locked")
	} else if ok {
		return nil
	}
	token, e := vault.New(dir).Get("admin-token")
	if e != nil {
		return errors.New("local_credential_missing")
	}
	client := localapi.Client{Address: c.Listen, Token: token}
	check, cancel := context.WithTimeout(ctx, 3*time.Second)
	data, statusErr := client.Post(check, "/admin/status", map[string]any{})
	cancel()
	var status struct {
		Device struct {
			ID string `json:"device_id"`
		} `json:"device"`
	}
	if statusErr != nil || json.Unmarshal(data, &status) != nil || status.Device.ID != c.DeviceID {
		if ok, _ := lock.TryLock(); ok {
			return nil
		}
		return errors.New("agent_shutdown_identity_unverified")
	}
	stop, cancel := context.WithTimeout(ctx, 3*time.Second)
	_, stopErr := client.Post(stop, "/admin/shutdown", map[string]any{})
	cancel()
	if stopErr != nil {
		if ok, _ := lock.TryLock(); ok {
			return nil
		}
		var response *localapi.ResponseError
		if errors.As(stopErr, &response) {
			switch response.StatusCode {
			case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented:
				return errors.New("legacy_agent_stop_required")
			default:
				return errors.New("agent_shutdown_refused")
			}
		}
		// A lost shutdown response may still have stopped the agent. Wait for
		// its lock to be released; never force termination or delete the lock.
	}
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		ok, e := lock.TryLock()
		if e != nil {
			return errors.New("existing_instance_locked")
		}
		if ok {
			lock.Unlock()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("agent_stop_timeout")
		case <-ticker.C:
		}
	}
}
func waitLocal(ctx context.Context, dir string, c config.Config, v vault.Vault) error {
	token, e := v.Get("admin-token")
	if e != nil {
		return errors.New("local_credential_missing")
	}
	probe, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		call, done := context.WithTimeout(probe, time.Second)
		_, e := (localapi.Client{Address: c.Listen, Token: token}).Post(call, "/admin/status", map[string]any{})
		done()
		if e == nil {
			return nil
		}
		select {
		case <-probe.Done():
			return errors.New("agent_health_failed")
		case <-ticker.C:
		}
	}
}
func ensureLocal(ctx context.Context, dir string, c config.Config, v vault.Vault) error {
	token, e := v.Get("admin-token")
	if e != nil {
		return errors.New("local_credential_missing")
	}
	check, cancel := context.WithTimeout(ctx, time.Second)
	_, e = (localapi.Client{Address: c.Listen, Token: token}).Post(check, "/admin/status", map[string]any{})
	cancel()
	if e == nil {
		return nil
	}
	exe, _ := os.Executable()
	if e = desktop.StartDetached(exe, "--state", dir, "serve"); e != nil {
		return e
	}
	return waitLocal(ctx, dir, c, v)
}
func launchOnboarding(ctx context.Context, dir string, c config.Config, v vault.Vault, args []string) error {
	f := flags("onboard")
	asJSON := f.Bool("json", false, "Return launch URL to a local caller (not a log)")
	if e := f.Parse(args); e != nil {
		return e
	}
	if e := ensureLocal(ctx, dir, c, v); e != nil {
		return e
	}
	token, _ := v.Get("admin-token")
	data, e := (localapi.Client{Address: c.Listen, Token: token}).Post(ctx, "/admin/onboard", map[string]any{})
	if e != nil {
		return e
	}
	var result struct {
		URL string `json:"url"`
	}
	if json.Unmarshal(data, &result) != nil {
		return errors.New("invalid_onboarding_response")
	}
	if *asJSON {
		output(result)
		return nil
	}
	return desktop.OpenURL(result.URL)
}
func repairLocal(ctx context.Context, dir string, c config.Config, v vault.Vault) error {
	if e := ensureLocal(ctx, dir, c, v); e != nil {
		return e
	}
	token, _ := v.Get("admin-token")
	_, e := (localapi.Client{Address: c.Listen, Token: token}).Post(ctx, "/admin/repair", map[string]any{})
	if e == nil {
		output(map[string]any{"repair_requested": true, "pause_unchanged": true})
	}
	return e
}

type instance struct {
	Name     string `json:"name"`
	StateDir string `json:"state_dir"`
}

func discoverInstances(selected string) []instance {
	candidates := []string{config.DefaultDir()}
	cwd, _ := os.Getwd()
	entries, _ := os.ReadDir(filepath.Join(cwd, ".state"))
	for _, e := range entries {
		if e.IsDir() {
			candidates = append(candidates, filepath.Join(cwd, ".state", e.Name()))
		}
	}
	out := []instance{}
	abs, _ := filepath.Abs(selected)
	for _, dir := range candidates {
		p, _ := filepath.Abs(dir)
		if p == abs {
			continue
		}
		if c, e := config.Load(p); e == nil {
			out = append(out, instance{Name: c.Name, StateDir: p})
		}
	}
	return out
}
