package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/dotcontrol/dotcontrol/internal/config"
	"github.com/dotcontrol/dotcontrol/internal/desktop"
	"github.com/dotcontrol/dotcontrol/internal/install"
	"github.com/zalando/go-keyring"
)

func activationBundle(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	data := []byte("fixture " + version)
	sum := sha256.Sum256(data)
	if err := os.WriteFile(filepath.Join(dir, agentName()), data, 0755); err != nil {
		t.Fatal(err)
	}
	m := install.Manifest{Version: version, OS: runtime.GOOS, Arch: runtime.GOARCH, Files: []install.File{{Path: agentName(), SHA256: hex.EncodeToString(sum[:]), Mode: 0755}}}
	b, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}
func activationFixture(t *testing.T) (string, string, []byte, installHooks) {
	t.Helper()
	keyring.MockInit()
	state := t.TempDir()
	root := filepath.Join(t.TempDir(), "program")
	c := config.New("existing", "original-driver")
	c.TunnelPath = "original-tunnel"
	c.TunnelID = "existing-tunnel"
	if err := config.Save(state, c); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(state, "config.json"))
	b = append(b, '\n')
	os.WriteFile(filepath.Join(state, "config.json"), b, 0600)
	h := defaultInstallHooks()
	h.stopUpgrade = func(context.Context, string) error { return nil }
	h.stop = func(context.Context, string) error { return nil }
	h.checkBinary = func(context.Context, string, string) error { return nil }
	h.start = func(string, ...string) error { return nil }
	h.autostart = func(string, string, bool) error { return nil }
	h.snapshot = func() (desktop.StartupSnapshot, error) {
		return desktop.StartupSnapshot{Exists: true, Value: "original startup"}, nil
	}
	h.restore = func(desktop.StartupSnapshot) error { return nil }
	h.health = func(context.Context, string, config.Config, string, string) error { return nil }
	h.restoredHealth = h.health
	h.waitPrograms = func(context.Context, []string) error { return nil }
	h.guard = func(context.Context, string) error { return nil }
	return state, root, b, h
}
func TestActivationRefusalDoesNotStopOrRewriteExistingState(t *testing.T) {
	state, root, original, h := activationFixture(t)
	stops := 0
	restores := 0
	h.stopUpgrade = func(context.Context, string) error { return errors.New("agent_shutdown_refused") }
	h.stop = func(context.Context, string) error { stops++; return nil }
	h.restore = func(desktop.StartupSnapshot) error { restores++; return nil }
	_, err := applyInstallation(context.Background(), installationRequest{State: state, Root: root, Bundle: activationBundle(t, "1.0"), NoStart: true}, h)
	if err == nil {
		t.Fatal("shutdown refusal accepted")
	}
	got, _ := os.ReadFile(filepath.Join(state, "config.json"))
	if string(got) != string(original) || stops != 0 || restores != 0 {
		t.Fatalf("refusal mutated state: stops=%d restores=%d", stops, restores)
	}
}
func TestUnmanagedActivationFailureRestoresExactConfigStartupAndLegacy(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("unmanaged migration is Windows-only")
	}
	state, root, original, h := activationFixture(t)
	legacy := filepath.Join(t.TempDir(), agentName())
	os.WriteFile(legacy, []byte("legacy"), 0755)
	h.legacy = func(context.Context, string) (legacyReference, error) {
		return legacyReference{Executable: legacy, SHA256: fileDigestForTest(t, legacy), Version: "0.1.0"}, nil
	}
	starts := []string{}
	h.start = func(p string, _ ...string) error { starts = append(starts, p); return nil }
	restored := false
	h.restore = func(s desktop.StartupSnapshot) error { restored = s.Value == "original startup"; return nil }
	h.health = func(_ context.Context, _ string, _ config.Config, p, _ string) error {
		if p != legacy {
			return errors.New("health rejected")
		}
		return nil
	}
	_, err := applyInstallation(context.Background(), installationRequest{State: state, Root: root, Bundle: activationBundle(t, "1.0"), Legacy: legacy}, h)
	if err == nil {
		t.Fatal("failed health accepted")
	}
	got, _ := os.ReadFile(filepath.Join(state, "config.json"))
	if string(got) != string(original) || !restored || len(starts) != 2 || starts[1] != legacy {
		t.Fatalf("legacy recovery incomplete: restored=%v starts=%v", restored, starts)
	}
}
func fileDigestForTest(t *testing.T, p string) string {
	t.Helper()
	b, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func TestCommittedCleanupFailureDoesNotRollback(t *testing.T) {
	state, root, _, h := activationFixture(t)
	req := installationRequest{State: state, Root: root, Bundle: activationBundle(t, "1.0"), NoStart: true}
	if _, err := applyInstallation(context.Background(), req, h); err != nil {
		t.Fatal(err)
	}
	stops := 0
	h.stop = func(context.Context, string) error { stops++; return nil }
	h.guard = func(context.Context, string) error { return errors.New("old program referenced") }
	req.Bundle = activationBundle(t, "2.0")
	r, err := applyInstallation(context.Background(), req, h)
	if !errors.Is(err, install.ErrCleanupPending) || !r.CleanupPending || r.Current.Version != "2.0" || stops != 0 {
		t.Fatalf("result=%+v err=%v stops=%d", r, err, stops)
	}
	h.guard = func(context.Context, string) error { return nil }
	req.Bundle = ""
	r, err = applyInstallation(context.Background(), req, h)
	if err != nil || r.Current.Version != "2.0" {
		t.Fatalf("cleanup-only retry failed: %+v %v", r, err)
	}
}
func TestInstalledHealthRequiresIdentityVersionAndExecutable(t *testing.T) {
	c := config.New("test", "")
	exe := filepath.Join(t.TempDir(), agentName())
	good := map[string]any{"device": map[string]any{"device_id": c.DeviceID, "version": "1.0"}, "runtime": map[string]any{"executable": exe, "pid": 123}}
	b, _ := json.Marshal(good)
	if !installedStatusMatches(b, c.DeviceID, "1.0", exe) {
		t.Fatal("matching runtime rejected")
	}
	for _, mutate := range []func(){func() { good["device"] = map[string]any{"device_id": "other", "version": "1.0"} }, func() { good["device"] = map[string]any{"device_id": c.DeviceID, "version": "0.9"} }, func() {
		good["device"] = map[string]any{"device_id": c.DeviceID, "version": "1.0"}
		good["runtime"] = map[string]any{"executable": exe + "-old", "pid": 123}
	}} {
		mutate()
		b, _ = json.Marshal(good)
		if installedStatusMatches(b, c.DeviceID, "1.0", exe) {
			t.Fatal("wrong runtime accepted")
		}
	}
}

func TestInterruptedUnmanagedActivationRecoversFromDurableSnapshot(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("unmanaged migration is Windows-only")
	}
	state, root, original, h := activationFixture(t)
	legacy := filepath.Join(t.TempDir(), agentName())
	if e := os.WriteFile(legacy, []byte("legacy"), 0755); e != nil {
		t.Fatal(e)
	}
	h.legacy = func(context.Context, string) (legacyReference, error) {
		return legacyReference{Executable: legacy, SHA256: fileDigestForTest(t, legacy), Version: "0.1.0"}, nil
	}
	h.health = func(context.Context, string, config.Config, string, string) error {
		panic("simulated crash after config change")
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("crash fixture did not run")
			}
		}()
		_, _ = applyInstallation(context.Background(), installationRequest{State: state, Root: root, Bundle: activationBundle(t, "1.0"), Legacy: legacy}, h)
	}()
	starts := []string{}
	h.start = func(p string, _ ...string) error { starts = append(starts, p); return nil }
	_, err := applyInstallation(context.Background(), installationRequest{State: state, Root: root}, h)
	if err == nil {
		t.Fatal("missing bundle should still fail after recovery")
	}
	got, _ := os.ReadFile(filepath.Join(state, "config.json"))
	if string(got) != string(original) || len(starts) != 1 || starts[0] != legacy {
		t.Fatalf("durable legacy recovery failed: starts=%v", starts)
	}
	if _, err := os.Stat(filepath.Join(root, "activation-recovery.json")); !os.IsNotExist(err) {
		t.Fatalf("recovery record not retired: %v", err)
	}
}

func TestNoStartPreservesUnknownConfigAndAvoidsStartupCredentials(t *testing.T) {
	state, root, original, h := activationFixture(t)
	var values map[string]any
	if e := json.Unmarshal(original, &values); e != nil {
		t.Fatal(e)
	}
	values["future_field"] = map[string]any{"preserve": "yes"}
	b, _ := json.Marshal(values)
	os.WriteFile(filepath.Join(state, "config.json"), b, 0600)
	h.snapshot = func() (desktop.StartupSnapshot, error) {
		t.Fatal("no-start read startup")
		return desktop.StartupSnapshot{}, nil
	}
	h.autostart = func(string, string, bool) error { t.Fatal("no-start changed startup"); return nil }
	h.start = func(string, ...string) error { t.Fatal("no-start launched runtime"); return nil }
	if _, err := applyInstallation(context.Background(), installationRequest{State: state, Root: root, Bundle: activationBundle(t, "1.0"), NoStart: true}, h); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(state, "config.json"))
	var after map[string]any
	json.Unmarshal(got, &after)
	if after["device_id"] != values["device_id"] || after["tunnel_id"] != values["tunnel_id"] || after["future_field"].(map[string]any)["preserve"] != "yes" {
		t.Fatal("configuration fields lost")
	}
}

func TestLegacyReferenceRejectsNonDotControlAndDirectories(t *testing.T) {
	p := filepath.Join(t.TempDir(), agentName())
	os.WriteFile(p, []byte("not executable"), 0755)
	for _, path := range []string{p, filepath.Dir(p), "relative.exe"} {
		if _, err := verifyLegacyExecutable(context.Background(), path); err == nil {
			t.Fatalf("unsafe legacy candidate accepted: %s", path)
		}
	}
}
