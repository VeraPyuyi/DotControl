//go:build !windows

package connections

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuthorizationScriptPreservesRestrictedEntryAndRefusesLinks(t *testing.T) {
	if b, e := exec.Command("stat", "-c", "%a", t.TempDir()).Output(); e != nil || len(b) == 0 {
		t.Skip("GNU stat required by Linux target")
	}
	run := func(home string) error {
		c := exec.Command("sh", "-c", authorizationCommand(PublicKey{Line: fixturePublicKey()}))
		c.Env = append(os.Environ(), "HOME="+home)
		return c.Run()
	}
	home := t.TempDir()
	_ = os.Chmod(home, 0700)
	if err := run(home); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(home, ".ssh", "authorized_keys")
	first, _ := os.ReadFile(p)
	if err := run(home); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(p)
	if string(first) != string(again) {
		t.Fatal("duplicate append")
	}
	commented := "# " + fixturePublicKey() + "\n"
	_ = os.WriteFile(p, []byte(commented), 0600)
	if err := run(home); err != nil {
		t.Fatal(err)
	}
	actual, _ := os.ReadFile(p)
	if string(actual) == commented {
		t.Fatal("comment suppressed missing key append")
	}
	restricted := `command="echo safe",restrict ` + fixturePublicKey() + "\n"
	_ = os.WriteFile(p, []byte(restricted), 0600)
	if err := run(home); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != restricted {
		t.Fatal("weakened existing restrictions")
	}
	_ = os.Chmod(p, 0666)
	if run(home) == nil {
		t.Fatal("accepted writable file")
	}
	_ = os.Chmod(p, 0600)
	target := filepath.Join(home, "other")
	_ = os.Rename(p, target)
	_ = os.Symlink(target, p)
	if run(home) == nil {
		t.Fatal("accepted symlink")
	}
	got, _ = os.ReadFile(target)
	if !strings.HasPrefix(string(got), restricted) {
		t.Fatal("modified linked file")
	}
}
