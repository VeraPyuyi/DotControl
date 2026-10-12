package main

import (
	"context"
	"github.com/dotcontrol/dotcontrol/internal/config"
	"github.com/dotcontrol/dotcontrol/internal/model"
	"testing"
)

func TestTerminalResumeDoesNotRequireDesktop(t *testing.T) {
	c := config.New("terminal", "missing")
	c.TerminalOnly, c.TerminalEnabled = true, true
	c.TerminalProfileID = "configured"
	if err := resumeReadiness(c, false, false, permissionStatus{State: "unknown"}, model.Activity{Available: false, Locked: true}); err != nil {
		t.Fatal(err)
	}
	c.TerminalEnabled = false
	if err := resumeReadiness(c, false, false, permissionStatus{}, model.Activity{}); err == nil || err.Error() != "terminal_not_enabled" {
		t.Fatalf("disabled: %v", err)
	}
	c.TerminalEnabled = true
	if err := resumeReadiness(c, true, false, permissionStatus{}, model.Activity{}); err == nil || err.Error() != "access_revoked" {
		t.Fatalf("revoked: %v", err)
	}
}

func TestLegacyResumeStillRequiresDesktop(t *testing.T) {
	c := config.New("legacy", "driver")
	if err := resumeReadiness(c, false, false, permissionStatus{}, model.Activity{}); err == nil {
		t.Fatal("legacy desktop guard removed")
	}
}

func TestTerminalStartupDoesNotStartActivityMonitor(t *testing.T) {
	c := config.New("terminal", "missing")
	c.TerminalOnly = true
	m := newRuntimeMonitor(context.Background(), c)
	defer m.Close()
	if s := m.Snapshot(); s.Available || s.Generation != 0 {
		t.Fatalf("terminal-only started monitor: %#v", s)
	}
}
