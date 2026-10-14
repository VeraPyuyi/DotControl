package config

import (
	"strings"
	"testing"
)

func TestTerminalModeIsExplicitAndSurvivesReload(t *testing.T) {
	dir := t.TempDir()
	c := New("device", "missing-driver")
	if c.TerminalOnly || c.TerminalEnabled {
		t.Fatal("legacy install enabled terminal delegation")
	}
	c.TerminalOnly = true
	c.TerminalEnabled = true
	c.TerminalProfileID = "my-project"
	c.TerminalServerID = "my-server"
	c.TerminalConfigHash = strings.Repeat("a", 64)
	if err := Save(dir, c); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil || got != c {
		t.Fatalf("reload: %#v %v", got, err)
	}
}

func TestTerminalEnableRequiresModeAndSelectedProfile(t *testing.T) {
	c := New("device", "")
	c.TerminalEnabled = true
	if err := Save(t.TempDir(), c); err == nil {
		t.Fatal("enabled without selecting terminal mode")
	}
	c.TerminalOnly = true
	if err := Save(t.TempDir(), c); err == nil {
		t.Fatal("enabled without a selected execution profile")
	}
	c.TerminalProfileID = "selected"
	if err := Save(t.TempDir(), c); err == nil {
		t.Fatal("enabled without validated configuration hash")
	}
}
