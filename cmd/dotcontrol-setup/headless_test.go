package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPayloadDestinationStaysEmptyAndRejectsExistingContents(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "payload")
	got, e := preparePayloadDestination(dest)
	if e != nil || !filepath.IsAbs(got) {
		t.Fatalf("destination=%s %v", got, e)
	}
	if _, e := os.Stat(dest); !os.IsNotExist(e) {
		t.Fatal("preflight created payload")
	}
	os.MkdirAll(dest, 0700)
	os.WriteFile(filepath.Join(dest, "retain"), []byte("retain"), 0600)
	if _, e := preparePayloadDestination(dest); e == nil {
		t.Fatal("nonempty destination accepted")
	}
}

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
