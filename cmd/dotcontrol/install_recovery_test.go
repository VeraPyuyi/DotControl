package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dotcontrol/dotcontrol/internal/config"
	"github.com/dotcontrol/dotcontrol/internal/desktop"
	"github.com/dotcontrol/dotcontrol/internal/install"
)

func managedRecoveryFixture(t *testing.T) (installationRequest, installHooks, install.Current) {
	t.Helper()
	state, root, _, h := activationFixture(t)
	req := installationRequest{State: state, Root: root, Bundle: activationBundle(t, "1.0"), NoStart: true}
	r, err := applyInstallation(context.Background(), req, h)
	if err != nil {
		t.Fatal(err)
	}
	req.NoStart = false
	return req, h, r.Current
}

func TestManagedUninstallAndReinstallPreservesState(t *testing.T) {
	req, h, _ := managedRecoveryFixture(t)
	configPath := filepath.Join(req.State, "config.json")
	original, _ := os.ReadFile(configPath)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(original, &fields); err != nil {
		t.Fatal(err)
	}
	fields["future_setting"] = json.RawMessage(`{"preserve":true}`)
	original, _ = json.Marshal(fields)
	if err := os.WriteFile(configPath, original, 0600); err != nil {
		t.Fatal(err)
	}
	preserved := map[string]string{"history_access.json": `{"mode":"allowlist","revision":3,"allowed_thread_ids":["fixture-a","fixture-b"]}`, "actions.jsonl": "old unknown receipt\n", "revoked": "true", "paused": "true"}
	for name, data := range preserved {
		if err := os.WriteFile(filepath.Join(req.State, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := install.Remove(context.Background(), install.RemoveOptions{Root: req.Root, StateDir: req.State, BeforeRemove: func(context.Context) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	// Managed uninstalls intentionally remove the startup entry.
	h.snapshot = func() (desktop.StartupSnapshot, error) { return desktop.StartupSnapshot{}, nil }
	starts, checks := 0, 0
	h.start = func(string, ...string) error { starts++; return nil }
	h.health = func(context.Context, string, config.Config, string, string) error { checks++; return nil }
	if _, err := applyInstallation(context.Background(), req, h); err != nil {
		t.Fatal(err)
	}
	if starts != 1 || checks != 1 {
		t.Fatalf("starts=%d checks=%d", starts, checks)
	}
	after, _ := os.ReadFile(configPath)
	var actual, expected map[string]any
	json.Unmarshal(after, &actual)
	json.Unmarshal(original, &expected)
	for k, v := range expected {
		if k != "driver_path" && k != "tunnel_path" && k != "activity_helper" && !reflect.DeepEqual(v, actual[k]) {
			t.Fatalf("configuration changed: %s", k)
		}
	}
	for name, data := range preserved {
		got, err := os.ReadFile(filepath.Join(req.State, name))
		if err != nil || string(got) != data {
			t.Fatalf("changed %s: %v", name, err)
		}
	}
}

func TestRetainedInstallCannotClaimDifferentState(t *testing.T) {
	req, h, _ := managedRecoveryFixture(t)
	if err := install.Remove(context.Background(), install.RemoveOptions{Root: req.Root, StateDir: req.State}); err != nil {
		t.Fatal(err)
	}
	other, _, _, _ := activationFixture(t)
	req.State = other
	h.snapshot = func() (desktop.StartupSnapshot, error) { return desktop.StartupSnapshot{}, nil }
	h.start = func(string, ...string) error { t.Fatal("foreign state activated"); return nil }
	if _, err := applyInstallation(context.Background(), req, h); err == nil || !strings.Contains(err.Error(), "install_state_binding_mismatch") {
		t.Fatal(err)
	}
}

func TestManagedMissingExecutableRepairAndFailedHealthRetry(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "healthy", true: "failed-health"}[failed], func(t *testing.T) {
			req, h, old := managedRecoveryFixture(t)
			previousExe := filepath.Join(old.Dir, agentName())
			if err := os.Remove(previousExe); err != nil {
				t.Fatal(err)
			}
			original, _ := os.ReadFile(filepath.Join(req.State, "config.json"))
			stopped, waited, restored := false, false, false
			h.stopUpgrade = func(context.Context, string, func([]int) error) error { stopped = true; return nil }
			h.waitPrograms = func(_ context.Context, paths []string) error {
				if !stopped || !slices.Contains(paths, previousExe) {
					t.Fatal("missing executable skipped exit check")
				}
				waited = true
				return nil
			}
			h.start = func(p string, _ ...string) error {
				if !waited || p == previousExe {
					t.Fatal("unconfirmed or missing old executable started")
				}
				return nil
			}
			h.restore = func(desktop.StartupSnapshot) error { restored = true; return nil }
			h.health = func(context.Context, string, config.Config, string, string) error {
				if failed {
					return errors.New("new_health_rejected")
				}
				return nil
			}
			r, err := applyInstallation(context.Background(), req, h)
			if !failed {
				if err != nil || r.Current.Dir == old.Dir {
					t.Fatalf("%+v %v", r, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "rollback_previous_program_unavailable") || !restored {
				t.Fatalf("rollback misreported: restored=%v err=%v", restored, err)
			}
			after, _ := os.ReadFile(filepath.Join(req.State, "config.json"))
			if string(original) != string(after) {
				t.Fatal("rollback lost exact config")
			}
			progress, e := install.ReadProgress(req.Root)
			if e != nil || progress.RecoveryNeeded {
				t.Fatalf("unrecoverable retry loop: %+v %v", progress, e)
			}
			h.health = func(context.Context, string, config.Config, string, string) error { return nil }
			if _, err = applyInstallation(context.Background(), req, h); err != nil {
				t.Fatal("repair retry blocked", err)
			}
		})
	}
}

func TestManagedMissingExecutableStillRequiresExitEvidence(t *testing.T) {
	req, h, old := managedRecoveryFixture(t)
	if err := os.Remove(filepath.Join(old.Dir, agentName())); err != nil {
		t.Fatal(err)
	}
	h.stopUpgrade = func(context.Context, string, func([]int) error) error { return errUpgradeExitUncertain }
	h.waitProcesses = func(context.Context, []int, func([]int) error) error {
		return errors.New("fixture_process_unconfirmed")
	}
	h.start = func(string, ...string) error { t.Fatal("started with uncertain exit"); return nil }
	if _, err := applyInstallation(context.Background(), req, h); err == nil {
		t.Fatal("uncertain stop ignored")
	}
}

func TestManagedUntrustedExecutableIsNotMissing(t *testing.T) {
	for _, directory := range []bool{false, true} {
		t.Run(map[bool]string{false: "modified", true: "directory"}[directory], func(t *testing.T) {
			req, h, old := managedRecoveryFixture(t)
			path := filepath.Join(old.Dir, agentName())
			if directory {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(path, []byte("untrusted"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			h.start = func(string, ...string) error { t.Fatal("untrusted previous accepted"); return nil }
			if _, err := applyInstallation(context.Background(), req, h); err == nil {
				t.Fatal("untrusted previous accepted")
			}
		})
	}
}

func TestMissingExecutableRepairRecoversInterruptedActivation(t *testing.T) {
	req, h, old := managedRecoveryFixture(t)
	missing := filepath.Join(old.Dir, agentName())
	if err := os.Remove(missing); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(req.State, "config.json")
	original, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	h.health = func(context.Context, string, config.Config, string, string) error { panic("simulated interruption") }
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected simulated interruption")
			}
		}()
		_, _ = applyInstallation(context.Background(), req, h)
	}()
	progress, err := install.ReadProgress(req.Root)
	if err != nil || !progress.RecoveryNeeded {
		t.Fatalf("missing recovery intent: %+v %v", progress, err)
	}
	restored := false
	h.restore = func(desktop.StartupSnapshot) error { restored = true; return nil }
	h.start = func(path string, _ ...string) error {
		if path == missing {
			t.Fatal("started missing old binary")
		}
		return nil
	}
	h.stopUpgrade = func(context.Context, string, func([]int) error) error {
		after, err := os.ReadFile(configPath)
		if err != nil || string(after) != string(original) || !restored {
			t.Fatal("new activation preceded restoration", err)
		}
		return nil
	}
	h.health = func(context.Context, string, config.Config, string, string) error { return nil }
	if _, err := applyInstallation(context.Background(), req, h); err != nil {
		t.Fatal(err)
	}
	progress, err = install.ReadProgress(req.Root)
	if err != nil || progress.RecoveryNeeded || progress.Phase != "complete" {
		t.Fatalf("%+v %v", progress, err)
	}
}
