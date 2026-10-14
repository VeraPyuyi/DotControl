//go:build darwin

package main

import (
	"context"
	"errors"
	"github.com/dotcontrol/dotcontrol/internal/config"
	"github.com/dotcontrol/dotcontrol/internal/desktop"
	"testing"
)

func TestMacRollbackRestoresComponentsBeforeStartupBootstrap(t *testing.T) {
	req, h, old := managedRecoveryFixture(t)
	req.Bundle = activationBundle(t, "2.0")
	componentDir := ""
	h.macComponents = func(_ context.Context, dir string) error { componentDir = dir; return nil }
	h.health = func(context.Context, string, config.Config, string, string) error { return errors.New("health failed") }
	restored := false
	h.restore = func(desktop.StartupSnapshot) error {
		if componentDir != old.Dir {
			t.Fatal("old agent could bootstrap before its fixed components were restored")
		}
		restored = true
		return nil
	}
	if _, e := applyInstallation(context.Background(), req, h); e == nil || !restored {
		t.Fatalf("rollback err=%v restored=%v", e, restored)
	}
}
