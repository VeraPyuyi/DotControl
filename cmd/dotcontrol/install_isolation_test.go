package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/dotcontrol/dotcontrol/internal/desktop"
	"github.com/dotcontrol/dotcontrol/internal/install"
	"github.com/gofrs/flock"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestRejectedStateDoesNotCreateInstallerLock(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	state := filepath.Join(root, "state")
	_, e := applyInstallation(context.Background(), installationRequest{State: state, Root: root, NoStart: true}, defaultInstallHooks())
	if e == nil {
		t.Fatal("overlapping state accepted")
	}
	if _, e = os.Stat(root); !os.IsNotExist(e) {
		t.Fatal("rejected path created lock or directory")
	}
}
func TestNormalInstallSerializesUserSettingsAcrossStates(t *testing.T) {
	base := t.TempDir()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("APPDATA", base)
	case "darwin":
		t.Setenv("HOME", base)
	default:
		t.Setenv("XDG_CONFIG_HOME", base)
	}
	release, e := lockInstallationSettings(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer release()
	state, root, _, h := activationFixture(t)
	h.globalLock = lockInstallationSettings
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, e = applyInstallation(ctx, installationRequest{State: state, Root: root, Bundle: activationBundle(t, "1.0")}, h)
	if e == nil {
		t.Fatal("normal install bypassed user settings lock")
	}
	if _, e = os.Stat(root); !os.IsNotExist(e) {
		t.Fatal("contending install changed program root")
	}
}
func TestStartupForAnotherStateIsPreserved(t *testing.T) {
	state, root, _, h := activationFixture(t)
	if _, e := applyInstallation(context.Background(), installationRequest{State: state, Root: root, Bundle: activationBundle(t, "0.9"), NoStart: true}, h); e != nil {
		t.Fatal(e)
	}
	h.snapshot = func() (desktop.StartupSnapshot, error) {
		return desktop.StartupSnapshot{Exists: true, Value: `"other agent" --state "other state" serve`}, nil
	}
	h.stopUpgrade = func(context.Context, string, func([]int) error) error {
		t.Fatal("stopped before startup selection")
		return nil
	}
	_, e := applyInstallation(context.Background(), installationRequest{State: state, Root: root, Bundle: activationBundle(t, "1.0")}, h)
	if e == nil || e.Error() != "startup_instance_selection_required" {
		t.Fatalf("startup conflict: %v", e)
	}
}

func TestNoStartRefusesOccupiedStateBeforeInstallerWrites(t *testing.T) {
	state, root, original, h := activationFixture(t)
	lock := flock.New(filepath.Join(state, "engine.lock"))
	ok, e := lock.TryLock()
	if e != nil || !ok {
		t.Fatal(e)
	}
	defer lock.Close()
	h.stopUpgrade = func(context.Context, string, func([]int) error) error { t.Fatal("shutdown invoked"); return nil }
	_, e = applyInstallation(context.Background(), installationRequest{State: state, Root: root, Bundle: activationBundle(t, "1.0"), NoStart: true}, h)
	if e == nil || e.Error() != "no_start_state_busy" {
		t.Fatalf("busy isolated state: %v", e)
	}
	if _, e = os.Stat(root); !os.IsNotExist(e) {
		t.Fatalf("busy state changed program root: %v", e)
	}
	b, _ := os.ReadFile(filepath.Join(state, "config.json"))
	if string(b) != string(original) {
		t.Fatal("busy state config changed")
	}
	if _, e = os.Stat(filepath.Join(state, "installer.lock")); !os.IsNotExist(e) {
		t.Fatal("busy state created installer lock")
	}
}

func TestRootIntegrationLockProtectsRecoveryAcrossStates(t *testing.T) {
	state, root, _, h := activationFixture(t)
	release, e := installationLock(context.Background(), filepath.Join(root, ".activation.lock"))
	if e != nil {
		t.Fatal(e)
	}
	defer release()
	if e := writeActivation(filepath.Join(root, "activation-recovery.json"), activationSnapshot{Schema: 1, State: "another-state", Phase: "activating"}); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, e = applyInstallation(ctx, installationRequest{State: state, Root: root, NoStart: true}, h)
	if e == nil || e.Error() != "installation_in_progress" {
		t.Fatalf("read journal outside root lock: %v", e)
	}
}

func TestActivatingRollbackPersistsNewRuntimeChildEvidence(t *testing.T) {
	state, root, original, h := activationFixture(t)
	os.MkdirAll(root, 0700)
	s := activationSnapshot{Schema: 1, State: state, Phase: "activating", Config: original, Processes: []int{100}}
	writeActivation(filepath.Join(root, "activation-recovery.json"), s)
	p := install.Progress{Phase: "health", RecoveryNeeded: true}
	b, _ := json.Marshal(p)
	os.WriteFile(filepath.Join(root, "transaction.json"), b, 0600)
	os.WriteFile(filepath.Join(root, ".dotcontrol-install.json"), []byte(`{"schema":1,"versions":[]}`), 0600)
	h.stop = func(_ context.Context, _ string, record func([]int) error) error {
		if e := record([]int{200, 201}); e != nil {
			t.Fatal(e)
		}
		return errUpgradeExitUncertain
	}
	_, e := applyInstallation(context.Background(), installationRequest{State: state, Root: root}, h)
	if e == nil {
		t.Fatal("uncertain new child accepted")
	}
	saved, e := loadActivation(filepath.Join(root, "activation-recovery.json"), state)
	if e != nil || len(saved.Processes) != 3 {
		t.Fatalf("new/old evidence lost: %+v %v", saved, e)
	}
	stopped := false
	h.stop = func(context.Context, string, func([]int) error) error { stopped = true; return nil }
	h.restore = func(desktop.StartupSnapshot) error { t.Fatal("restored startup with pending child"); return nil }
	h.waitProcesses = func(_ context.Context, ids []int, _ func([]int) error) error {
		if !stopped {
			t.Fatal("wait prevented safe shutdown retry")
		}
		if len(ids) != 3 {
			t.Fatal("retry did not reload evidence")
		}
		return errors.New("child remains")
	}
	_, e = applyInstallation(context.Background(), installationRequest{State: state, Root: root}, h)
	if e == nil {
		t.Fatal("retry discarded uncertainty")
	}
}

func TestInterruptedRollbackRestoresConfigAtomically(t *testing.T) {
	state, root, original, h := activationFixture(t)
	os.MkdirAll(root, 0700)
	current := append(append([]byte{}, original...), []byte("\n\n")...)
	configPath := filepath.Join(state, "config.json")
	if e := os.WriteFile(configPath, current, 0600); e != nil {
		t.Fatal(e)
	}
	linked := filepath.Join(t.TempDir(), "previous-config-inode")
	if e := os.Link(configPath, linked); e != nil {
		t.Skipf("hard-link fixture unavailable: %v", e)
	}
	s := activationSnapshot{Schema: 1, State: state, Phase: "activating", Config: original, NoStart: true}
	if e := writeActivation(filepath.Join(root, "activation-recovery.json"), s); e != nil {
		t.Fatal(e)
	}
	p := install.Progress{Phase: "health", RecoveryNeeded: true}
	b, _ := json.Marshal(p)
	os.WriteFile(filepath.Join(root, "transaction.json"), b, 0600)
	os.WriteFile(filepath.Join(root, ".dotcontrol-install.json"), []byte(`{"schema":1,"versions":[]}`), 0600)
	h.stop = func(context.Context, string, func([]int) error) error {
		t.Fatal("no-start recovery used lifecycle")
		return nil
	}
	_, e := applyInstallation(context.Background(), installationRequest{State: state, Root: root, NoStart: true}, h)
	if e == nil {
		t.Fatal("missing bundle accepted after recovery")
	}
	restored, _ := os.ReadFile(configPath)
	unchanged, _ := os.ReadFile(linked)
	if string(restored) != string(original) {
		t.Fatal("original bytes not restored")
	}
	if string(unchanged) != string(current) {
		t.Fatal("rollback truncated original inode instead of replacing atomically")
	}
}
func TestNoStartRefusesNormalInterruptedRecovery(t *testing.T) {
	state, root, _, h := activationFixture(t)
	os.MkdirAll(root, 0700)
	if e := writeActivation(filepath.Join(root, "activation-recovery.json"), activationSnapshot{Schema: 1, State: state, Phase: "activating", NoStart: false}); e != nil {
		t.Fatal(e)
	}
	h.stop = func(context.Context, string, func([]int) error) error {
		t.Fatal("normal recovery shutdown invoked")
		return nil
	}
	_, e := applyInstallation(context.Background(), installationRequest{State: state, Root: root, NoStart: true}, h)
	if e == nil || e.Error() != "no_start_recovery_requires_runtime" {
		t.Fatalf("normal recovery allowed: %v", e)
	}
}
func TestWaitingRecoveryRetainsChildEvidenceAfterAgentLockReleased(t *testing.T) {
	state, root, original, h := activationFixture(t)
	os.MkdirAll(root, 0700)
	s := activationSnapshot{Schema: 1, State: state, Phase: "waiting", Config: original, Processes: []int{12345}}
	if e := writeActivation(filepath.Join(root, "activation-recovery.json"), s); e != nil {
		t.Fatal(e)
	}
	p := install.Progress{Phase: "waiting_for_exit", RecoveryNeeded: true}
	b, _ := json.Marshal(p)
	os.WriteFile(filepath.Join(root, "transaction.json"), b, 0600)
	os.WriteFile(filepath.Join(root, ".dotcontrol-install.json"), []byte(`{"schema":1,"versions":[]}`), 0600)
	checked := false
	h.waitProcesses = func(_ context.Context, ids []int, record func([]int) error) error {
		checked = true
		if len(ids) != 1 || ids[0] != 12345 {
			t.Fatal("lost child evidence")
		}
		if e := record([]int{12345, 23456}); e != nil {
			t.Fatal(e)
		}
		return errors.New("owned_child_still_running")
	}
	h.start = func(string, ...string) error { t.Fatal("restarted while child unconfirmed"); return nil }
	_, e := applyInstallation(context.Background(), installationRequest{State: state, Root: root}, h)
	if e == nil {
		t.Fatal("live child accepted")
	}
	if !checked {
		t.Fatalf("did not inspect persisted child evidence: %v", e)
	}
	if _, e = os.Stat(filepath.Join(root, "activation-recovery.json")); e != nil {
		t.Fatal("waiting evidence discarded")
	}
	retained, e := loadActivation(filepath.Join(root, "activation-recovery.json"), state)
	if e != nil || len(retained.Processes) != 2 || retained.Processes[1] != 23456 {
		t.Fatalf("new descendant evidence lost: %+v %v", retained, e)
	}
}
func TestDistinctRootsSerializeSameState(t *testing.T) {
	state, root, _, h := activationFixture(t)
	other := filepath.Join(t.TempDir(), "other")
	lock, e := lockInstallationState(context.Background(), state)
	if e != nil {
		t.Fatal(e)
	}
	defer lock()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, e = applyInstallation(ctx, installationRequest{State: state, Root: other, Bundle: activationBundle(t, "1.0"), NoStart: true}, h)
	if e == nil {
		t.Fatal("another root bypassed state lock")
	}
	if _, e = os.Stat(other); !os.IsNotExist(e) {
		t.Fatal("competing install wrote files")
	}
	_ = root
}
