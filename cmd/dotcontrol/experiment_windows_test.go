//go:build windows

package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/codexbridge"
	"github.com/dotcontrol/dotcontrol/internal/connections"
	"github.com/dotcontrol/dotcontrol/internal/experiment"
)

func TestExperimentGUISubsystemWritesRedirectedJSONWithoutAgent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "dotcontrol helper.exe")
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go.exe")
	if out, e := exec.CommandContext(ctx, goBinary, "build", "-ldflags=-H=windowsgui", "-o", binary, ".").CombinedOutput(); e != nil {
		t.Fatalf("GUI build %v %s", e, out)
	}
	state := filepath.Join(t.TempDir(), "state with 'quote' $dollar;")
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
	// Exercise the exact shell command supplied to official exec_command. A
	// direct GUI exe invocation is insufficient: PowerShell can return before
	// the process exits and lose both its JSON and actual exit code.
	for _, shell := range []string{"pwsh.exe", "powershell.exe"} {
		shellPath, lookupErr := exec.LookPath(shell)
		if lookupErr != nil {
			if shell == "powershell.exe" {
				t.Fatal(lookupErr)
			}
			t.Log("optional PowerShell 7 unavailable")
			continue
		}
		for _, failure := range []bool{false, true} {
			name := "success"
			if failure {
				name = "failure"
			}
			t.Run(shell+"_"+name, func(t *testing.T) {
				id := "never-launched"
				if failure {
					id = "../invalid"
				}
				args := []string{binary, "--state", state, "experiment", "stop", "--profile", "fixture", "--experiment-id", id}
				command := codexbridge.ExperimentPowerShellCommand(args)
				out, runErr := exec.CommandContext(ctx, shellPath, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", command).CombinedOutput()
				if failure {
					exit, ok := runErr.(*exec.ExitError)
					if !ok || exit.ExitCode() != 1 || strings.TrimSpace(string(out)) != "DotControl: invalid_experiment_request" {
						t.Fatalf("GUI shell failure lost exit/error: %s %v", out, runErr)
					}
				} else {
					var result experiment.Status
					if runErr != nil || json.Unmarshal(out, &result) != nil || result.State != "absent" || result.Code != "stop_unavailable" {
						t.Fatalf("GUI shell did not wait/capture JSON: %s %v", out, runErr)
					}
				}
			})
		}
	}
}
