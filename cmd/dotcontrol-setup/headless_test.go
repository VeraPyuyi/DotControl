package main

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestHeadlessArgumentsAreBoundedAndIsolated(t *testing.T) {
	base := t.TempDir()
	state := filepath.Join(base, "device state")
	root := filepath.Join(base, "program")
	legacy := filepath.Join(base, "old agent.exe")
	a, err := parseSetupArgs([]string{"--install", "--state", state, "--program-root", root, "--no-start", "--legacy-executable", legacy})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--state", state, "repair", "--bundle", "payload", "--program-root", root, "--no-start", "--legacy-executable", legacy}
	if got := setupInstallArgs(a, "payload"); !reflect.DeepEqual(got, want) {
		t.Fatalf("args=%q", got)
	}
	for _, args := range [][]string{{"--install"}, {"--install", "--state", "relative", "--program-root", root}, {"--install", "--state", state, "--program-root", root, "--bundle", "external"}, {"--no-start"}, {"--verify-payload", "--install"}, {"--install", "--state", state, "--program-root", root, "serve"}} {
		if _, err := parseSetupArgs(args); err == nil {
			t.Fatalf("unsafe arguments accepted: %q", args)
		}
	}
}
func TestCleanupPendingExplainsNewVersionStaysActive(t *testing.T) {
	if got := installationMessage("DotControl: cleanup_pending\nold generation referenced"); got == "DotControl: cleanup_pending\nold generation referenced" {
		t.Fatal("missing cleanup pending presentation")
	}
}
