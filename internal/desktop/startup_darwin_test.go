//go:build darwin

package desktop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLaunchAgentLifecycleUsesNoRunningBootout(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	old := launchCommand
	t.Cleanup(func() { launchCommand = old })
	state := t.TempDir()
	exe := filepath.Join(t.TempDir(), "中文 agent")
	loaded, running, disabled := false, false, false
	var events []string
	launchCommand = func(_ context.Context, args ...string) ([]byte, error) {
		events = append(events, strings.Join(args, " "))
		switch args[0] {
		case "print-disabled":
			if disabled {
				return []byte(`"org.dotcontrol.agent" => true`), nil
			}
			return []byte("{}"), nil
		case "print":
			if !loaded {
				return []byte("Could not find service"), errors.New("missing")
			}
			value := "path = " + managedPlistPath() + "\nprogram = " + exe + "\narguments = {\n" + exe + "\n--state\n" + state + "\nserve\n--managed-start\nlaunch-agent\n}\n"
			if running {
				value += "pid = 123\n"
			}
			return []byte(value), nil
		case "disable":
			disabled = true
		case "enable":
			disabled = false
		case "bootstrap":
			loaded = true
		case "bootout":
			if running {
				t.Fatal("bootout signalled running agent")
			}
			loaded = false
		case "kickstart":
			running = true
		default:
			t.Fatal("unexpected launchctl", args)
		}
		return nil, nil
	}
	if e := ConfigureStartup(exe, state, false); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(plistPath()); !os.IsNotExist(e) {
		t.Fatal("disabled login got persistent LaunchAgent")
	}
	if e := StartManaged(exe, state); e != nil {
		t.Fatal(e)
	}
	s, e := SnapshotStartup()
	if e != nil || !s.Loaded || s.AutostartConfigured() {
		t.Fatalf("%+v %v", s, e)
	}
	running = true
	beforeRefusal, e := SnapshotStartup()
	if e != nil {
		t.Fatal(e)
	}
	if e := SuspendStartup(state); e != nil {
		t.Fatal(e)
	}
	if !disabled {
		t.Fatal("restart not suspended")
	}
	if e := RestoreStartup(beforeRefusal); e != nil {
		t.Fatal("refused-stop restoration", e)
	}
	if disabled || !running {
		t.Fatal("refusal changed running original or retained disabled override")
	}
	if e := ResumeStartup(state); e != nil {
		t.Fatal(e)
	}
	if e := SuspendStartup(state); e != nil {
		t.Fatal(e)
	}
	if e := ConfigureStartup(exe, state, true); e == nil {
		t.Fatal("replaced running loaded job")
	}
	running = false
	if e := ConfigureStartup(exe, state, true); e != nil {
		t.Fatal(e)
	}
	if e := StartManaged(exe, state); e != nil {
		t.Fatal(e)
	}
	running = false
	if e := RestoreStartup(s); e != nil {
		t.Fatal(e)
	}
	restored, e := SnapshotStartup()
	if e != nil || !restored.Loaded || restored.AutostartConfigured() {
		t.Fatalf("restoration: %+v %v", restored, e)
	}
	if e := RemoveStartup(state); e != nil {
		t.Fatal(e)
	}
	if loaded {
		t.Fatal("uninstall retained loaded service")
	}
	for _, event := range events {
		if strings.Contains(event, "kill") || strings.Contains(event, "-k") {
			t.Fatal(event)
		}
	}
}

func TestLoadedLaunchAgentSameExecutableDifferentStateIsRejected(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	old := launchCommand
	t.Cleanup(func() { launchCommand = old })
	exe := "/fixture/agent"
	state := t.TempDir()
	if e := writeStartupFile(managedPlistPath(), launchAgentXML(launchAgentLabel, exe, state), 0600); e != nil {
		t.Fatal(e)
	}
	launchCommand = func(_ context.Context, args ...string) ([]byte, error) {
		if args[0] == "print-disabled" {
			return []byte("{}"), nil
		}
		return []byte("path = " + managedPlistPath() + "\nprogram = " + exe + "\narguments = {\n" + exe + "\n--state\n/foreign/state\nserve\n--managed-start\nlaunch-agent\n}\n"), nil
	}
	if _, e := SnapshotStartup(); e == nil || e.Error() != "launch_agent_loaded_definition_unknown" {
		t.Fatal("foreign loaded state accepted", e)
	}
}
func TestLoadedLaunchAgentReferencesSurviveMissingPlist(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	old := launchCommand
	t.Cleanup(func() { launchCommand = old })
	root := filepath.Join(t.TempDir(), "old-generation")
	launchCommand = func(_ context.Context, args ...string) ([]byte, error) {
		if len(args) == 2 && strings.HasSuffix(args[1], "/fixture.external") {
			return []byte("program = " + filepath.Join(root, "dotcontrol")), nil
		}
		return []byte("services = {\n 0 - fixture.external\n}\n"), nil
	}
	text, e := startupReferenceText(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if e = checkProgramReferences(root, nil, text); e == nil {
		t.Fatal("loaded external reference missed")
	}
}
