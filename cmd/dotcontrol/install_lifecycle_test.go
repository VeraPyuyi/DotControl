package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dotcontrol/dotcontrol/internal/config"
	"github.com/dotcontrol/dotcontrol/internal/desktop"
	"github.com/gofrs/flock"
)

func TestUpgradeSuspendsBeforeExitAndStartsRegisteredRuntime(t *testing.T) {
	req, h, _ := managedRecoveryFixture(t)
	req.Bundle = activationBundle(t, "2.0")
	var events []string
	h.suspend = func(state string) error {
		if state != req.State {
			t.Fatal("state namespace changed")
		}
		events = append(events, "suspend")
		return nil
	}
	h.stopUpgrade = func(context.Context, string, func([]int) error) error { events = append(events, "exit"); return nil }
	h.waitPrograms = func(context.Context, []string) error { events = append(events, "exited"); return nil }
	h.autostart = func(string, string, bool) error { events = append(events, "register"); return nil }
	h.resume = func(string) error { events = append(events, "resume"); return nil }
	h.start = func(string, ...string) error { events = append(events, "start"); return nil }
	h.health = func(context.Context, string, config.Config, string, string) error {
		events = append(events, "health")
		return nil
	}
	h.guard = func(context.Context, string) error { events = append(events, "cleanup"); return nil }
	if _, e := applyInstallation(context.Background(), req, h); e != nil {
		t.Fatal(e)
	}
	want := []string{"suspend", "exit", "exited", "register", "resume", "start", "health", "cleanup"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events=%v", events)
	}
}
func TestNoStartEntriesRejectSupervisorBackoff(t *testing.T) {
	for _, command := range []string{"install", "repair", "uninstall"} {
		t.Run(command, func(t *testing.T) {
			req, _, _ := managedRecoveryFixture(t)
			lock := flock.New(filepath.Join(req.State, "background.lock"))
			ok, e := lock.TryLock()
			if e != nil || !ok {
				t.Fatal(e)
			}
			defer lock.Close()
			before, _ := os.ReadFile(filepath.Join(req.State, "config.json"))
			args := []string{"--program-root", req.Root, "--no-start"}
			if command != "uninstall" {
				args = append(args, "--bundle", activationBundle(t, "2.0"))
			}
			e = installation(context.Background(), req.State, command, args)
			if e == nil || e.Error() != "no_start_state_busy" {
				t.Fatalf("backoff entry allowed: %v", e)
			}
			after, _ := os.ReadFile(filepath.Join(req.State, "config.json"))
			if string(before) != string(after) {
				t.Fatal("busy state changed")
			}
		})
	}
}
func TestNoStartHoldsBothRuntimeLocksDuringActivation(t *testing.T) {
	req, h, _ := managedRecoveryFixture(t)
	req.NoStart = true
	req.Bundle = activationBundle(t, "2.0")
	h.checkBinary = func(context.Context, string, string) error {
		for _, name := range []string{"background.lock", "engine.lock"} {
			lock := flock.New(filepath.Join(req.State, name))
			ok, e := lock.TryLock()
			lock.Close()
			if e != nil || ok {
				t.Fatalf("activation did not hold %s: %v", name, e)
			}
		}
		return nil
	}
	if _, e := applyInstallation(context.Background(), req, h); e != nil {
		t.Fatal(e)
	}
}

func TestFailedHealthSuspendsCandidateBeforeRollbackExit(t *testing.T) {
	req, h, _ := managedRecoveryFixture(t)
	req.Bundle = activationBundle(t, "2.0")
	var events []string
	h.suspend = func(string) error { events = append(events, "suspend"); return nil }
	h.stop = func(context.Context, string, func([]int) error) error {
		events = append(events, "candidate-exit")
		return nil
	}
	h.restore = func(desktop.StartupSnapshot) error { events = append(events, "restore"); return nil }
	h.resume = func(string) error { events = append(events, "resume"); return nil }
	h.health = func(context.Context, string, config.Config, string, string) error {
		events = append(events, "health-failed")
		return errors.New("fixture")
	}
	if _, e := applyInstallation(context.Background(), req, h); e == nil {
		t.Fatal("health failure accepted")
	}
	want := []string{"suspend", "resume", "health-failed", "suspend", "candidate-exit", "resume", "restore"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events=%v", events)
	}
}

func TestUncertainExitRetainsSuspensionAndRecoveryJournal(t *testing.T) {
	req, h, _ := managedRecoveryFixture(t)
	req.Bundle = activationBundle(t, "2.0")
	h.stopUpgrade = func(context.Context, string, func([]int) error) error { return errUpgradeExitUncertain }
	h.waitProcesses = func(context.Context, []int, func([]int) error) error { return errors.New("still present") }
	h.restore = func(desktop.StartupSnapshot) error { t.Fatal("restored during uncertain stop"); return nil }
	h.resume = func(string) error { t.Fatal("resumed during uncertain stop"); return nil }
	if _, e := applyInstallation(context.Background(), req, h); e == nil {
		t.Fatal("uncertain stop accepted")
	}
	if _, e := os.Stat(filepath.Join(req.Root, "activation-recovery.json")); e != nil {
		t.Fatal("journal lost", e)
	}
}

func TestUpgradePreservesDisabledLoginChoiceButStillRegistersSupervisor(t *testing.T) {
	req, h, _ := managedRecoveryFixture(t)
	h.snapshot = func() (desktop.StartupSnapshot, error) { return desktop.StartupSnapshot{}, nil }
	registered := false
	h.autostart = func(_ string, state string, login bool) error {
		if login || state != req.State {
			t.Fatal("disabled choice changed")
		}
		registered = true
		return nil
	}
	h.start = func(string, ...string) error {
		if !registered {
			t.Fatal("detached before registration")
		}
		return nil
	}
	if _, e := applyInstallation(context.Background(), req, h); e != nil {
		t.Fatal(e)
	}
}

func TestInstalledHealthRejectsUnmanagedMatchingProcess(t *testing.T) {
	exe := filepath.Join(t.TempDir(), agentName())
	status := map[string]any{"device": map[string]any{"device_id": "fixture", "version": "2.0"}, "runtime": map[string]any{"pid": 20, "executable": exe}, "background": desktop.StartupStatus{Configured: true, Mechanism: "task-scheduler", Managed: false}}
	b, _ := json.Marshal(status)
	if installedManagedStatusMatches(b, "fixture", "2.0", exe) {
		t.Fatal("unmanaged runtime accepted")
	}
	status["background"] = desktop.StartupStatus{Mechanism: "task-scheduler", Managed: true}
	b, _ = json.Marshal(status)
	if !installedManagedStatusMatches(b, "fixture", "2.0", exe) {
		t.Fatal("on-demand supervised runtime rejected")
	}
}

func TestNoStartUninstallEntryRetainsState(t *testing.T) {
	req, h, _ := managedRecoveryFixture(t)
	_ = h
	before, e := os.ReadFile(filepath.Join(req.State, "config.json"))
	if e != nil {
		t.Fatal(e)
	}
	if e = installation(context.Background(), req.State, "uninstall", []string{"--program-root", req.Root, "--no-start"}); e != nil {
		t.Fatal(e)
	}
	after, e := os.ReadFile(filepath.Join(req.State, "config.json"))
	if e != nil || string(before) != string(after) {
		t.Fatal("retained config changed")
	}
}
