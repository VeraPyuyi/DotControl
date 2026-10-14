package main

import (
	"context"
	"errors"
	"github.com/dotcontrol/dotcontrol/internal/config"
	"github.com/dotcontrol/dotcontrol/internal/desktop"
	"os"
	"path/filepath"
	"testing"
)

func TestRollbackWaitsForCandidateSupervisorBeforeRestore(t *testing.T) {
	req, h, _ := managedRecoveryFixture(t)
	req.Bundle = activationBundle(t, "2.0")
	failed := false
	candidate := ""
	resumes := 0
	h.start = func(exe string, _ ...string) error { candidate = exe; return nil }
	h.health = func(context.Context, string, config.Config, string, string) error {
		failed = true
		return errors.New("health failed")
	}
	h.waitPrograms = func(_ context.Context, paths []string) error {
		if !failed {
			return nil
		}
		found := false
		for _, p := range paths {
			if p == candidate {
				found = true
			}
		}
		if !found {
			t.Fatal("candidate supervisor omitted from exit check")
		}
		return errors.New("supervisor still exiting")
	}
	h.resume = func(string) error {
		resumes++
		if resumes > 1 {
			t.Fatal("rollback resumed before supervisor exit")
		}
		return nil
	}
	h.restore = func(desktop.StartupSnapshot) error {
		t.Fatal("restored startup before candidate supervisor exit")
		return nil
	}
	if _, e := applyInstallation(context.Background(), req, h); e == nil {
		t.Fatal("uncertain supervisor exit accepted")
	}
	if _, e := os.Stat(filepath.Join(req.Root, "activation-recovery.json")); e != nil {
		t.Fatal("recovery evidence lost", e)
	}
}
