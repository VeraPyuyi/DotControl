package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/codexbridge"
	"github.com/dotcontrol/dotcontrol/internal/config"
	"github.com/dotcontrol/dotcontrol/internal/connections"
	"github.com/dotcontrol/dotcontrol/internal/model"
	"github.com/dotcontrol/dotcontrol/internal/onboard"
)

type terminalCheck struct {
	Available bool   `json:"available"`
	Code      string `json:"code"`
	Nonce     string `json:"nonce,omitempty"`
}

// The local one-click path selects only the official workspace profile, never
// full access. SSH and experiment setup are optional later task concerns.
func basicTerminalProfile(dir string) connections.ExecutionProfile {
	return connections.ExecutionProfile{ID: "terminal-default", Name: "本机 Codex 任务", ProjectDir: filepath.Join(dir, "tasks"), PermissionProfile: ":workspace", ApprovalPolicy: "never", Execution: "terminal", Presentation: "visible"}
}

func (o *runtimeOwner) connectBasicTerminal(ctx context.Context, store *connections.Store) (any, error) {
	if !o.eng.Device().Paused {
		return nil, errors.New("terminal_mode_pause_required")
	}
	if err := o.invalidateTerminalConfiguration(); err != nil {
		return nil, err
	}
	c, err := store.Load()
	if err != nil {
		return nil, err
	}
	profile := basicTerminalProfile(o.dir)
	for _, p := range c.ExecutionProfiles {
		if p.ID == profile.ID && (p.ProjectDir != profile.ProjectDir || p.Execution != "terminal" || p.PermissionProfile != ":workspace" || p.ApprovalPolicy != "never") {
			return nil, errors.New("terminal_profile_requires_manual_selection")
		}
		if p.ID == profile.ID {
			profile = p
		}
	}
	found, err := connections.DiscoverCodex(ctx, c.CodexExecutable, connections.DiscoveryOptions{})
	if err != nil {
		return nil, err
	}
	if !found.Available || found.Version != codexbridge.SupportedAppServerVersion {
		return nil, errors.New("codex_version_unsupported")
	}
	if err = os.MkdirAll(profile.ProjectDir, 0700); err != nil {
		return nil, errors.New("task_directory_unavailable")
	}
	if err = store.SetCodexExecutable(found.Executable); err != nil {
		return nil, err
	}
	if _, err = store.UpsertExecution(profile); err != nil {
		return nil, err
	}
	return o.configureTerminal(ctx, onboard.ConnectionsRequest{Operation: "enable_terminal", ProfileID: profile.ID})
}

func terminalCheckCommand(ctx context.Context, dir string, args []string) error {
	f := flags("terminal-check")
	profile := f.String("profile", "", "Saved execution profile")
	server := f.String("server-profile", "", "Saved server profile")
	if err := f.Parse(args); err != nil {
		return err
	}
	if *profile == "" || len(f.Args()) != 0 {
		return errors.New("invalid_terminal_check")
	}
	r, _, err := checkTerminal(ctx, dir, *profile, *server)
	if err != nil {
		return err
	}
	output(r)
	if !r.Available {
		return errors.New(r.Code)
	}
	return nil
}

func (o *runtimeOwner) invalidateTerminalConfiguration() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.cfg.TerminalEnabled {
		return nil
	}
	if !o.eng.Device().Paused {
		return errors.New("terminal_mode_pause_required")
	}
	if err := o.bridge.ConfigureTerminal(true, false, "", "", ""); err != nil {
		return err
	}
	next := o.cfg
	next.TerminalEnabled = false
	if err := config.Save(o.dir, next); err != nil {
		_ = o.bridge.ConfigureTerminal(true, true, o.cfg.TerminalProfileID, o.cfg.TerminalServerID, o.cfg.TerminalConfigHash)
		return errors.New("config_save_failed")
	}
	o.cfg = next
	return nil
}

func terminalCheckCLI(ctx context.Context, dir string, args []string) error {
	f := flags("terminal-check-helper")
	server := f.String("server-profile", "", "Saved local server reference")
	nonce := f.String("nonce", "", "Local probe correlation")
	if err := f.Parse(args); err != nil {
		return err
	}
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(*nonce) || len(f.Args()) != 0 {
		return errors.New("invalid_terminal_check")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return errors.New("task_directory_unavailable")
	}
	result := runTerminalHelper(ctx, filepath.Join(dir, "connections"), cwd, *server, *nonce, connections.PreflightSSH)
	output(result)
	// Failure is carried in this sole JSON receipt, so GUI shell wrappers do
	// not mix an additional stderr line into the parseable response.
	return nil
}

// The installed helper runs INSIDE the selected official command sandbox. It
// proves local access and SSH, but never submits a remote job or edits SSH trust.
func runTerminalHelper(ctx context.Context, connectionsDir, cwd, server, nonce string, preflight func(context.Context, connections.ServerProfile, connections.PreflightOptions) connections.Status) terminalCheck {
	r := terminalCheck{Code: "terminal_probe_failed", Nonce: nonce}
	dirs := []string{cwd}
	// Basic delegation needs only its owned task directory. The optional fixed
	// experiment helper additionally needs the device-side experiment journal.
	if server != "" {
		dirs = append(dirs, filepath.Dir(connectionsDir))
	}
	for _, dir := range dirs {
		f, err := os.CreateTemp(dir, ".dotcontrol-write-check-")
		if err != nil {
			r.Code = "terminal_permission_denied"
			return r
		}
		name := f.Name()
		_, writeErr := f.WriteString(nonce)
		syncErr := f.Sync()
		closeErr := f.Close()
		removeErr := os.Remove(name)
		if writeErr != nil || syncErr != nil || closeErr != nil || removeErr != nil {
			r.Code = "terminal_permission_denied"
			return r
		}
	}
	if server != "" {
		c, err := connections.NewStore(connectionsDir).Load()
		if err != nil {
			return r
		}
		found := false
		for _, p := range c.ServerProfiles {
			if p.ID == server {
				found = true
				s := preflight(ctx, p, connections.PreflightOptions{})
				if !s.Available {
					r.Code = s.Code
					return r
				}
				if p.RemoteDir == "" {
					r.Code = "ssh_remote_dir_unavailable"
					return r
				}
			}
		}
		if !found {
			r.Code = "server_profile_not_found"
			return r
		}
	}
	r.Available = true
	r.Code = "terminal_ready"
	return r
}

func terminalProbeResult(exit int, out, nonce string) terminalCheck {
	r := terminalCheck{}
	if json.Unmarshal([]byte(strings.TrimSpace(out)), &r) != nil || r.Nonce != nonce || (r.Available && exit != 0) {
		return terminalCheck{Code: "terminal_probe_unconfirmed"}
	}
	r.Nonce = ""
	switch r.Code {
	case "terminal_ready":
		if r.Available && exit == 0 {
			return r
		}
	case "terminal_server_required", "terminal_permission_denied", "ssh_auth_failed", "ssh_identity_unavailable", "ssh_unknown_host", "ssh_host_key_changed", "ssh_network_error", "ssh_timeout", "ssh_remote_dir_unavailable", "ssh_unavailable", "ssh_preflight_failed", "server_profile_not_found", "ssh_remote_check_unconfirmed", "ssh_cancelled":
		return terminalCheck{Code: r.Code}
	}
	return terminalCheck{Code: "terminal_probe_failed"}
}

func terminalCheckArgs(binary, dir, server, nonce string) []string {
	args := []string{binary, "--state", dir, "terminal-check-helper", "--nonce", nonce}
	// Windows PowerShell 5.1 drops an empty native argument. Omit the optional
	// flag entirely rather than letting it consume the following nonce flag.
	if server != "" {
		args = append(args, "--server-profile", server)
	}
	return args
}

func checkTerminal(ctx context.Context, dir, profile, server string) (terminalCheck, string, error) {
	c, err := connections.NewStore(filepath.Join(dir, "connections")).Load()
	if err != nil {
		return terminalCheck{}, "", err
	}
	binding := codexbridge.TerminalConfigHash(c)
	var p connections.ExecutionProfile
	for _, v := range c.ExecutionProfiles {
		if v.ID == profile {
			p = v
		}
	}
	if p.ID == "" || p.Execution != "terminal" || p.ApprovalPolicy != "never" {
		return terminalCheck{}, binding, errors.New("terminal_needs_local_setup")
	}
	if server != "" {
		found := false
		for _, s := range c.ServerProfiles {
			if s.ID == server {
				found = true
			}
		}
		if !found {
			return terminalCheck{}, binding, errors.New("server_profile_not_found")
		}
	}
	found, err := connections.DiscoverCodex(ctx, c.CodexExecutable, connections.DiscoveryOptions{})
	if err != nil {
		return terminalCheck{}, binding, err
	}
	if !found.Available || found.Version != codexbridge.SupportedAppServerVersion {
		return terminalCheck{}, binding, errors.New("codex_version_unsupported")
	}
	cwd, err := codexbridge.ProbeWorkspace(p.ProjectDir)
	if err != nil {
		return terminalCheck{}, binding, errors.New("task_directory_unavailable")
	}
	bounded, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	client, err := codexbridge.Start(bounded, found.Executable, cwd)
	if err != nil {
		return terminalCheck{}, binding, errors.New("codex_connection_failed")
	}
	defer client.Close()
	if err = client.Call(bounded, "initialize", map[string]any{"clientInfo": map[string]string{"name": "dotcontrol_terminal_check", "version": model.Version}, "capabilities": map[string]bool{"experimentalApi": true}}, nil); err != nil {
		return terminalCheck{}, binding, errors.New("codex_connection_failed")
	}
	if err = client.Notify(bounded, "initialized", map[string]any{}); err != nil {
		return terminalCheck{}, binding, err
	}
	profiles, err := codexbridge.ReadPermissionProfiles(bounded, client, cwd)
	if err != nil {
		return terminalCheck{}, binding, errors.New("permission_inventory_unavailable")
	}
	allowed := false
	for _, v := range profiles {
		if v.ID == p.PermissionProfile && v.Allowed {
			allowed = true
		}
	}
	if !allowed {
		return terminalCheck{}, binding, errors.New("permission_profile_unavailable")
	}
	binary, err := os.Executable()
	if err != nil {
		return terminalCheck{}, binding, err
	}
	nonce := config.Random()
	command := terminalCheckArgs(binary, dir, server, nonce)
	if runtime.GOOS == "windows" {
		shell, err := exec.LookPath("powershell.exe")
		if err != nil {
			return terminalCheck{}, binding, errors.New("powershell_unavailable")
		}
		command = []string{shell, "-NoProfile", "-NonInteractive", "-Command", codexbridge.ExperimentPowerShellCommand(command)}
	}
	var response struct {
		ExitCode int    `json:"exitCode"`
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
	}
	err = client.Call(bounded, "command/exec", map[string]any{"command": command, "cwd": cwd, "permissionProfile": p.PermissionProfile, "timeoutMs": 35000}, &response)
	if err != nil {
		code := "terminal_probe_failed"
		if strings.Contains(strings.ToLower(err.Error()), "split writable roots") {
			code = "terminal_sandbox_layout_unsupported"
		}
		return terminalCheck{Code: code}, binding, nil
	}
	return terminalProbeResult(response.ExitCode, response.Stdout, nonce), binding, nil
}

// Only the protected local wizard can select/enable terminal mode. A mode
// switch invalidates desktop observations and never changes the global pause.
func (o *runtimeOwner) configureTerminal(ctx context.Context, request onboard.ConnectionsRequest) (any, error) {
	if request.Operation == "check_terminal" {
		r, _, err := checkTerminal(ctx, o.dir, request.ProfileID, request.ServerProfileID)
		return r, err
	}
	if !o.eng.Device().Paused {
		return nil, errors.New("terminal_mode_pause_required")
	}
	enabled := request.Operation == "enable_terminal"
	var binding string
	if enabled {
		r, b, err := checkTerminal(ctx, o.dir, request.ProfileID, request.ServerProfileID)
		if err != nil {
			return nil, err
		}
		if !r.Available {
			return r, nil
		}
		binding = b
	}
	o.componentsMu.Lock()
	defer o.componentsMu.Unlock()
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.revoked {
		return nil, errors.New("access_revoked")
	}
	if !o.eng.Device().Paused {
		return nil, errors.New("terminal_mode_pause_required")
	}
	if enabled {
		c, err := connections.NewStore(filepath.Join(o.dir, "connections")).Load()
		if err != nil {
			return nil, err
		}
		if codexbridge.TerminalConfigHash(c) != binding {
			return nil, errors.New("terminal_configuration_changed")
		}
	}
	old := o.cfg
	next := old
	next.TerminalOnly = true
	next.TerminalEnabled = enabled
	next.TerminalProfileID = request.ProfileID
	next.TerminalServerID = request.ServerProfileID
	next.TerminalConfigHash = binding
	if err := o.bridge.ConfigureTerminal(true, enabled, request.ProfileID, request.ServerProfileID, next.TerminalConfigHash); err != nil {
		return nil, err
	}
	if err := config.Save(o.dir, next); err != nil {
		_ = o.bridge.ConfigureTerminal(old.TerminalOnly, old.TerminalEnabled, old.TerminalProfileID, old.TerminalServerID, old.TerminalConfigHash)
		return nil, errors.New("config_save_failed")
	}
	o.cfg = next
	o.monitor.Close()
	if err := o.eng.Reconnect(ctx, func() model.Driver { return unavailableDriver{} }); err != nil {
		return nil, err
	}
	o.native = nil
	o.driverDiagnostic = nil
	o.driverError = ""
	o.permissions = permissionStatus{State: "not_required"}
	if enabled {
		if err := o.eng.Pause(false); err != nil {
			return nil, err
		}
	}
	o.requestRepair()
	capability := o.bridge.Capabilities().CodexTerminal
	code := "terminal_mode_selected"
	if enabled {
		code = "terminal_enabled"
		if !capability.Available {
			code = "terminal_enabled_not_ready"
		}
	}
	return map[string]any{"enabled": enabled, "available": capability.Available, "code": code, "capability": capability}, nil
}
