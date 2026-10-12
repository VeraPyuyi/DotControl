//go:build windows

package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/connections"
	"github.com/dotcontrol/dotcontrol/internal/experiment"
)

func TestExperimentGUISubsystemWritesRedirectedJSONWithoutAgent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "dotcontrol.exe")
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go.exe")
	if out, e := exec.CommandContext(ctx, goBinary, "build", "-ldflags=-H=windowsgui", "-o", binary, ".").CombinedOutput(); e != nil {
		t.Fatalf("GUI build %v %s", e, out)
	}
	state := t.TempDir()
	_, e := connections.NewStore(filepath.Join(state, "connections")).UpsertServer(connections.ServerProfile{ID: "fixture", Name: "Fictional fixture", SSHAlias: "example.invalid", RemoteDir: "/tmp"})
	if e != nil {
		t.Fatal(e)
	}
	// stop on an unknown ID must produce JSON without contacting any SSH server.
	raw, e := exec.CommandContext(ctx, binary, "--state", state, "experiment", "stop", "--profile", "fixture", "--experiment-id", "never-launched").Output()
	var s experiment.Status
	if e != nil || json.Unmarshal(raw, &s) != nil || s.State != "absent" || s.Code != "stop_unavailable" {
		t.Fatalf("GUI pipe: %s %v", raw, e)
	}
}
