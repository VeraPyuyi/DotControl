//go:build darwin

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
)

func startMacSessionComponents(ctx context.Context, state string) {
	home, _ := os.UserHomeDir()
	exe, _ := os.Executable()
	menu := exec.CommandContext(ctx, filepath.Join(home, "Applications", "DotControl.app", "Contents", "MacOS", "DotControlMenu"), exe, state)
	menu.Stdout = nil
	menu.Stderr = nil
	if menu.Start() == nil {
		go menu.Wait()
	}
	// The stable upstream app is the TCC authorization host. Never launch a
	// copied CLI at a different path or modify its signature to obtain access.
	if verifyCua(ctx, "/Applications/CuaDriver.app") == nil {
		_ = exec.CommandContext(ctx, "/usr/bin/open", "-g", "/Applications/CuaDriver.app", "--args", "serve").Run()
	}
}
