package main

import (
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/config"
	"github.com/dotcontrol/dotcontrol/internal/desktop"
	"github.com/dotcontrol/dotcontrol/internal/install"
	"github.com/dotcontrol/dotcontrol/internal/localapi"
	"github.com/dotcontrol/dotcontrol/internal/vault"
	"github.com/gofrs/flock"
)

type installationRequest struct {
	State, Root, Bundle, Name, Legacy string
	NoStart                           bool
	Progress                          func(install.Progress)
}
type legacyReference struct {
	Executable string `json:"executable"`
	SHA256     string `json:"sha256"`
	Version    string `json:"version"`
}
type activationSnapshot struct {
	Schema    int                     `json:"schema"`
	State     string                  `json:"state"`
	Phase     string                  `json:"phase"`
	Config    []byte                  `json:"config,omitempty"`
	Startup   desktop.StartupSnapshot `json:"startup"`
	Previous  *legacyReference        `json:"previous,omitempty"`
	Target    string                  `json:"target"`
	NoStart   bool                    `json:"no_start"`
	Processes []int                   `json:"processes,omitempty"`
}
type installHooks struct {
	stopUpgrade    func(context.Context, string, func([]int) error) error
	stop           func(context.Context, string, func([]int) error) error
	globalLock     func(context.Context) (func(), error)
	waitProcesses  func(context.Context, []int, func([]int) error) error
	checkBinary    func(context.Context, string, string) error
	legacy         func(context.Context, string) (legacyReference, error)
	start          func(string, ...string) error
	autostart      func(string, string, bool) error
	snapshot       func() (desktop.StartupSnapshot, error)
	restore        func(desktop.StartupSnapshot) error
	health         func(context.Context, string, config.Config, string, string) error
	restoredHealth func(context.Context, string, config.Config, string, string) error
	waitPrograms   func(context.Context, []string) error
	guard          func(context.Context, string) error
}

func defaultInstallHooks() installHooks {
	return installHooks{stopUpgrade: stopForUpgradeWithEvidence, stop: stopForUpgradeWithEvidence, globalLock: lockInstallationSettings, waitProcesses: desktop.WaitProcessIDs, checkBinary: checkInstalledBinary, legacy: verifyLegacyExecutable, start: desktop.StartDetached, autostart: desktop.Autostart, snapshot: desktop.SnapshotStartup, restore: desktop.RestoreStartup, health: waitInstalledLocal, restoredHealth: waitRestoredLocal, waitPrograms: desktop.WaitExecutableExit, guard: desktop.CheckProgramReferences}
}

func checkInstalledBinary(ctx context.Context, exe, version string) error {
	check, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(check, exe, "version")
	desktop.HideConsole(cmd)
	b, e := cmd.Output()
	if e != nil || !validInstalledVersion(b, version) {
		return errors.New("agent_binary_health_failed")
	}
	return nil
}
func regularExecutableDigest(p string) (string, error) {
	if !filepath.IsAbs(p) {
		return "", errors.New("legacy_executable_absolute_path_required")
	}
	for q := filepath.Clean(p); ; q = filepath.Dir(q) {
		s, e := os.Lstat(q)
		if e != nil || s.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("legacy_executable_unsafe")
		}
		if q == filepath.Clean(p) && !s.Mode().IsRegular() {
			return "", errors.New("legacy_executable_unsafe")
		}
		if filepath.Dir(q) == q {
			break
		}
	}
	resolved, e := filepath.EvalSymlinks(p)
	if e != nil || !sameProgramPath(resolved, p) {
		return "", errors.New("legacy_executable_unsafe")
	}
	f, e := os.Open(p)
	if e != nil {
		return "", e
	}
	defer f.Close()
	sum := sha256.New()
	if _, e = io.Copy(sum, f); e != nil {
		return "", e
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}
func verifyLegacyExecutable(ctx context.Context, p string) (legacyReference, error) {
	digest, e := regularExecutableDigest(p)
	if e != nil {
		return legacyReference{}, e
	}
	info, e := buildinfo.ReadFile(p)
	if e != nil || info.Path != "github.com/dotcontrol/dotcontrol/cmd/dotcontrol" {
		return legacyReference{}, errors.New("legacy_executable_not_dotcontrol")
	}
	check, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(check, p, "version")
	desktop.HideConsole(cmd)
	b, e := cmd.Output()
	version := strings.TrimSuffix(string(b), "\n")
	if e != nil || !regexp.MustCompile(`^[0-9][A-Za-z0-9._-]{0,79}$`).MatchString(version) {
		return legacyReference{}, errors.New("legacy_executable_version_invalid")
	}
	again, e := regularExecutableDigest(p)
	if e != nil || again != digest {
		return legacyReference{}, errors.New("legacy_executable_changed")
	}
	return legacyReference{Executable: filepath.Clean(p), SHA256: digest, Version: version}, nil
}
func sameProgramPath(a, b string) bool {
	a = filepath.Clean(strings.TrimPrefix(a, `\\?\`))
	b = filepath.Clean(strings.TrimPrefix(b, `\\?\`))
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
func writeActivation(p string, s activationSnapshot) error {
	b, e := json.Marshal(s)
	if e != nil {
		return e
	}
	return writePrivateAtomic(p, b)
}
func writePrivateAtomic(p string, b []byte) error {
	f, e := os.CreateTemp(filepath.Dir(p), ".activation-")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	return os.Rename(tmp, p)
}
func loadActivation(p, state string) (*activationSnapshot, error) {
	info, e := os.Lstat(p)
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil || !info.Mode().IsRegular() || info.Size() > 2<<20 {
		return nil, errors.New("activation_recovery_invalid")
	}
	b, e := os.ReadFile(p)
	if e != nil {
		return nil, e
	}
	var s activationSnapshot
	if json.Unmarshal(b, &s) != nil || s.Schema != 1 || !sameProgramPath(s.State, state) || (s.Phase != "waiting" && s.Phase != "activating") {
		return nil, errors.New("activation_recovery_invalid")
	}
	if len(s.Processes) > 65536 {
		return nil, errors.New("activation_recovery_invalid")
	}
	for _, pid := range s.Processes {
		if pid <= 0 || pid > 1<<31-1 {
			return nil, errors.New("activation_recovery_invalid")
		}
	}
	return &s, nil
}
func removeActivation(p string) error {
	e := os.Remove(p)
	if os.IsNotExist(e) {
		return nil
	}
	return e
}

func recordActivationProcesses(journal string, s *activationSnapshot, ids []int) error {
	union := map[int]bool{}
	for _, pid := range s.Processes {
		union[pid] = true
	}
	for _, pid := range ids {
		if pid <= 0 || pid > 1<<31-1 {
			return errors.New("process_evidence_invalid")
		}
		union[pid] = true
	}
	if len(union) > 65536 {
		return errors.New("process_evidence_invalid")
	}
	s.Processes = s.Processes[:0]
	for pid := range union {
		s.Processes = append(s.Processes, pid)
	}
	sort.Ints(s.Processes)
	return writeActivation(journal, *s)
}

func applyInstallation(ctx context.Context, r installationRequest, h installHooks) (install.Result, error) {
	if e := install.ValidatePaths(r.Root, r.State); e != nil {
		return install.Result{}, e
	}
	if r.NoStart {
		if e := checkNoStartBusy(r.State); e != nil {
			return install.Result{}, e
		}
	}
	unlock, e := lockInstallationState(ctx, r.State)
	if e != nil {
		return install.Result{}, e
	}
	defer unlock()
	if r.NoStart {
		engineLock := flock.New(filepath.Join(r.State, "engine.lock"))
		defer engineLock.Close()
		ok, e := engineLock.TryLock()
		if e != nil {
			return install.Result{}, e
		}
		if !ok {
			return install.Result{}, errors.New("no_start_state_busy")
		}
	} else {
		release, e := h.globalLock(ctx)
		if e != nil {
			return install.Result{}, e
		}
		defer release()
	}
	releaseRoot, e := installationLock(ctx, filepath.Join(r.Root, ".activation.lock"))
	if e != nil {
		return install.Result{}, e
	}
	defer releaseRoot()
	journal := filepath.Join(r.Root, "activation-recovery.json")
	if r.NoStart {
		pending, e := loadActivation(journal, r.State)
		if e != nil {
			return install.Result{}, e
		}
		if pending != nil && !pending.NoStart {
			return install.Result{}, errors.New("no_start_recovery_requires_runtime")
		}
	}
	rollback := func(ctx context.Context, previous *install.Current) error {
		s, e := loadActivation(journal, r.State)
		if e != nil || s == nil {
			return e
		}
		if r.NoStart && !s.NoStart {
			return errors.New("no_start_recovery_requires_runtime")
		}
		if s.Phase == "waiting" {
			if e = h.waitProcesses(ctx, s.Processes, func(ids []int) error { return recordActivationProcesses(journal, s, ids) }); e != nil {
				return e
			}
			if e = h.waitPrograms(ctx, snapshotProgramPaths(s)); e != nil {
				return e
			}
			lock := flock.New(filepath.Join(r.State, "engine.lock"))
			defer lock.Close()
			ok, e := lock.TryLock()
			if e != nil {
				return e
			}
			if !ok {
				return errors.New("agent_exit_recovery_pending")
			}
			_ = lock.Unlock()
		} else {
			if !s.NoStart {
				if e = h.stop(ctx, r.State, func(ids []int) error { return recordActivationProcesses(journal, s, ids) }); e != nil {
					return e
				}
				if e = h.waitProcesses(ctx, s.Processes, func(ids []int) error { return recordActivationProcesses(journal, s, ids) }); e != nil {
					return e
				}
			}
			if len(s.Config) > 0 {
				if e = writePrivateAtomic(filepath.Join(r.State, "config.json"), s.Config); e != nil {
					return e
				}
			} else {
				if e = os.Remove(filepath.Join(r.State, "config.json")); e != nil && !os.IsNotExist(e) {
					return e
				}
			}
			if !s.NoStart {
				if e = h.restore(s.Startup); e != nil {
					return e
				}
			}
		}
		if s.Previous != nil && !s.NoStart {
			digest, e := regularExecutableDigest(s.Previous.Executable)
			if e != nil || digest != s.Previous.SHA256 {
				return errors.New("rollback_executable_changed")
			}
			if runtime.GOOS == "darwin" && previous != nil {
				if e = installMacComponents(ctx, previous.Dir); e != nil {
					return e
				}
			}
			if e = h.start(s.Previous.Executable, "--state", r.State, "serve"); e != nil {
				return e
			}
			c, e := config.Load(r.State)
			if e != nil {
				return e
			}
			if e = h.restoredHealth(ctx, r.State, c, s.Previous.Executable, s.Previous.Version); e != nil {
				return e
			}
		}
		return removeActivation(journal)
	}
	before := func(ctx context.Context, target install.Current) error {
		s := activationSnapshot{Schema: 1, State: r.State, Phase: "waiting", Target: target.Dir, NoStart: r.NoStart}
		b, e := os.ReadFile(filepath.Join(r.State, "config.json"))
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		s.Config = b
		if !r.NoStart {
			if s.Startup, e = h.snapshot(); e != nil {
				return e
			}
			if s.Startup.Exists && !startupBelongsToState(s.Startup, r.State) {
				return errors.New("startup_instance_selection_required")
			}
		}
		previous, e := install.ReadCurrent(r.Root)
		if e == nil {
			p := filepath.Join(previous.Dir, agentName())
			digest, de := regularExecutableDigest(p)
			if de != nil {
				return de
			}
			s.Previous = &legacyReference{Executable: p, SHA256: digest, Version: previous.Version}
		} else if !os.IsNotExist(e) {
			return e
		}
		if r.Legacy != "" {
			if runtime.GOOS != "windows" {
				return errors.New("legacy_migration_windows_only")
			}
			if s.Previous != nil {
				return errors.New("legacy_reference_with_managed_install")
			}
			ref, e := h.legacy(ctx, r.Legacy)
			if e != nil {
				return e
			}
			s.Previous = &ref
		}
		if len(s.Config) > 0 && s.Previous == nil && !r.NoStart {
			return errors.New("legacy_executable_required")
		}
		if r.NoStart {
			s.Phase = "activating"
		}
		if e = writeActivation(journal, s); e != nil {
			return e
		}
		if r.NoStart {
			return nil
		}
		if e = h.stopUpgrade(ctx, r.State, func(ids []int) error { return recordActivationProcesses(journal, &s, ids) }); e != nil {
			if errors.Is(e, errUpgradeExitUncertain) {
				return e
			}
			return errors.Join(e, removeActivation(journal))
		}
		if e = h.waitPrograms(ctx, snapshotProgramPaths(&s)); e != nil {
			return e
		}
		s.Phase = "activating"
		return writeActivation(journal, s)
	}
	health := func(ctx context.Context, target install.Current) error {
		exe := filepath.Join(target.Dir, agentName())
		if e := h.checkBinary(ctx, exe, target.Version); e != nil {
			return e
		}
		s, e := loadActivation(journal, r.State)
		if e != nil || s == nil {
			return errors.New("activation_recovery_missing")
		}
		var c config.Config
		if len(s.Config) > 0 {
			if e = json.Unmarshal(s.Config, &c); e != nil {
				return e
			}
		} else {
			c = config.New(r.Name, "")
			listener, e := net.Listen("tcp", "127.0.0.1:0")
			if e != nil {
				return errors.New("local_port_unavailable")
			}
			c.Listen = listener.Addr().String()
			listener.Close()
		}
		c.DriverPath = filepath.Join(target.Dir, "cua-driver.exe")
		c.TunnelPath = filepath.Join(target.Dir, "tunnel-client.exe")
		if runtime.GOOS == "darwin" {
			if !r.NoStart {
				if e = installMacComponents(ctx, target.Dir); e != nil {
					return e
				}
			}
			home, _ := os.UserHomeDir()
			c.DriverPath = "/Applications/CuaDriver.app/Contents/MacOS/cua-driver"
			c.ActivityHelper = filepath.Join(home, "Applications", "DotControlActivity.app", "Contents", "MacOS", "DotControlActivity")
			c.TunnelPath = filepath.Join(target.Dir, "tunnel-client")
		}
		if e = saveProgramConfig(r.State, s.Config, c); e != nil {
			return errors.New("config_save_failed")
		}
		if r.NoStart {
			return nil
		}
		v := vault.New(r.State)
		for _, key := range []string{"admin-token", "mcp-token"} {
			if e = vault.Ensure(v, key, config.Random); e != nil {
				return errors.New("credential_store_failed")
			}
		}
		if len(s.Config) == 0 || s.Startup.Exists {
			if e = h.autostart(exe, r.State, true); e != nil {
				return errors.New("autostart_unavailable")
			}
		}
		if e = h.start(exe, "--state", r.State, "serve"); e != nil {
			return e
		}
		return h.health(ctx, r.State, c, exe, target.Version)
	}
	result, err := install.Repair(ctx, install.Options{Root: r.Root, StateDir: r.State, BundleDir: r.Bundle, BeforeActivate: before, Health: health, Rollback: rollback, CleanupOldVersions: true, BeforeCleanup: func(ctx context.Context, c install.Current) error { return h.guard(ctx, c.Dir) }, Progress: r.Progress})
	if err == nil || errors.Is(err, install.ErrCleanupPending) {
		if e := removeActivation(journal); e != nil {
			result.CleanupPending = true
			return result, errors.Join(install.ErrCleanupPending, err, e)
		}
	}
	return result, err
}

// Only program locations change during upgrade. Unknown config fields survive.
func saveProgramConfig(dir string, original []byte, c config.Config) error {
	if len(original) == 0 {
		return config.Save(dir, c)
	}
	var fields map[string]json.RawMessage
	if e := json.Unmarshal(original, &fields); e != nil {
		return e
	}
	for k, v := range map[string]string{"driver_path": c.DriverPath, "tunnel_path": c.TunnelPath} {
		fields[k], _ = json.Marshal(v)
	}
	if runtime.GOOS == "darwin" {
		fields["activity_helper"], _ = json.Marshal(c.ActivityHelper)
	}
	b, e := json.MarshalIndent(fields, "", "  ")
	if e != nil {
		return e
	}
	tmp := filepath.Join(dir, "config.json.tmp")
	if e = os.WriteFile(tmp, b, 0600); e != nil {
		return e
	}
	return os.Rename(tmp, filepath.Join(dir, "config.json"))
}
func installedStatusMatches(b []byte, id, version, exe string) bool {
	var s struct {
		Device struct {
			ID      string `json:"device_id"`
			Version string `json:"version"`
		} `json:"device"`
		Runtime struct {
			PID        int    `json:"pid"`
			Executable string `json:"executable"`
		} `json:"runtime"`
	}
	return json.Unmarshal(b, &s) == nil && s.Device.ID == id && s.Device.Version == version && s.Runtime.PID > 0 && sameProgramPath(s.Runtime.Executable, exe)
}
func waitInstalledLocal(ctx context.Context, dir string, c config.Config, exe, version string) error {
	return waitRuntimeLocal(ctx, dir, c, exe, version, false)
}

// Older agents lack runtime metadata. Restoration additionally verifies that
// the explicitly recorded executable is running; new activation never uses it.
func waitRestoredLocal(ctx context.Context, dir string, c config.Config, exe, version string) error {
	return waitRuntimeLocal(ctx, dir, c, exe, version, true)
}
func waitRuntimeLocal(ctx context.Context, dir string, c config.Config, exe, version string, legacy bool) error {
	token, e := vault.New(dir).Get("admin-token")
	if e != nil {
		return errors.New("local_credential_missing")
	}
	probe, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		call, done := context.WithTimeout(probe, time.Second)
		b, e := (localapi.Client{Address: c.Listen, Token: token}).Post(call, "/admin/status", map[string]any{})
		done()
		if e == nil && installedStatusMatches(b, c.DeviceID, version, exe) {
			return nil
		}
		if e == nil && legacy {
			var s struct {
				Device struct {
					ID      string `json:"device_id"`
					Version string `json:"version"`
				} `json:"device"`
				Runtime json.RawMessage `json:"runtime"`
			}
			if json.Unmarshal(b, &s) == nil && s.Device.ID == c.DeviceID && s.Device.Version == version && len(s.Runtime) == 0 {
				if running, e := desktop.ExecutableRunning(probe, exe); e == nil && running {
					return nil
				}
			}
		}
		select {
		case <-probe.Done():
			return errors.New("agent_health_identity_failed")
		case <-ticker.C:
		}
	}
}
