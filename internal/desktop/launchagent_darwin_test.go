//go:build darwin

package desktop

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type launchFixture struct {
	m                                        launchAgent
	job                                      agentJob
	loaded, running, disabled, failBootstrap bool
	calls                                    []string
}

func newLaunchFixture(t *testing.T) *launchFixture {
	t.Helper()
	f := &launchFixture{}
	f.m = launchAgent{path: filepath.Join(t.TempDir(), "agent.plist"), uid: os.Getuid(), run: f.run}
	return f
}
func (f *launchFixture) run(args ...string) ([]byte, error) {
	f.calls = append(f.calls, args[0])
	switch args[0] {
	case "print-disabled":
		return []byte(fmt.Sprintf("disabled services = {\n \"%s\" => %t\n}", agentLabel, f.disabled)), nil
	case "print":
		if !f.loaded {
			return []byte("Could not find service"), errors.New("missing")
		}
		state := "waiting"
		if f.running {
			state = "running"
		}
		return []byte(fmt.Sprintf("%s = {\n path = %s\n program = %s\n state = %s\n arguments = {\n %s\n }\n}", f.m.target(), f.m.path, f.job.Program, state, strings.Join(f.job.Args, "\n "))), nil
	case "bootstrap":
		if f.loaded {
			return nil, errors.New("already loaded")
		}
		if f.failBootstrap {
			f.failBootstrap = false
			return nil, errors.New("mock load failure")
		}
		f.job, _, _ = f.m.read()
		f.loaded, f.running = true, true
		return nil, nil
	case "bootout":
		f.loaded, f.running = false, false
		return nil, nil
	case "kickstart":
		f.running = true
		return nil, nil
	default:
		return nil, errors.New("unexpected command")
	}
}
func TestLaunchAgentFileIsNotRegistrationAndReopenDoesNotDuplicate(t *testing.T) {
	f := newLaunchFixture(t)
	exe, state := "/test/program/versions/v1/dotcontrol", "/test/state"
	if err := f.m.write(jobData(exe, state)); err != nil {
		t.Fatal(err)
	}
	s := f.m.status(exe, state)
	if !s.Configured || s.Registered || s.Enabled() || s.Reason != "not_registered" {
		t.Fatal("file mistaken for registered service")
	}
	if err := f.m.start(exe, state, false); err != nil {
		t.Fatal(err)
	}
	calls := len(f.calls)
	if err := f.m.start(exe, state, false); err != nil {
		t.Fatal(err)
	}
	for _, call := range f.calls[calls:] {
		if call == "bootstrap" || call == "kickstart" || call == "bootout" {
			t.Fatal("second launch of running service")
		}
	}
	if !f.m.status(exe, state).Enabled() {
		t.Fatal("registered service not recognized")
	}
	data, _ := os.ReadFile(f.m.path)
	if !strings.Contains(string(data), "AssociatedBundleIdentifiers") || !strings.Contains(string(data), permissionHost) {
		t.Fatal("permission host association missing")
	}
}
func TestLaunchAgentUpgradeLoadFailureAndRollback(t *testing.T) {
	f := newLaunchFixture(t)
	old, next, state := "/test/program/versions/v1/dotcontrol", "/test/program/versions/v2/dotcontrol", "/test/state"
	if err := f.m.start(old, state, true); err != nil {
		t.Fatal(err)
	}
	f.running = false // Caller already completed authenticated graceful stop.
	f.failBootstrap = true
	if err := f.m.start(next, state, true); err == nil || err.Error() != "autostart_load_failed" {
		t.Fatal("load failure hidden")
	}
	if err := f.m.start(old, state, true); err != nil {
		t.Fatal(err)
	}
	if f.job.Program != old || !f.loaded {
		t.Fatal("rollback did not restore original service")
	}
	f.running = false
	if err := f.m.start(next, state, true); err != nil {
		t.Fatal(err)
	}
	if f.job.Program != next {
		t.Fatal("upgrade kept stale binary")
	}
}
func TestLaunchAgentRefusesWrongStatePathDisabledAndUnownedJobs(t *testing.T) {
	for _, kind := range []string{"state", "program", "loaded_program", "disabled", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			f := newLaunchFixture(t)
			exe, state := "/test/program/versions/v1/dotcontrol", "/test/state"
			if err := f.m.start(exe, state, true); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "state":
				state = "/other/state"
			case "program":
				exe = "/other/versions/v1/dotcontrol"
			case "loaded_program":
				f.job.Program = "/wrong/dotcontrol"
			case "disabled":
				f.disabled = true
			case "symlink":
				original := f.m.path + ".original"
				if err := os.Rename(f.m.path, original); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(original, f.m.path); err != nil {
					t.Fatal(err)
				}
			}
			before := len(f.calls)
			if kind != "program" {
				if err := f.m.validate(state); err == nil {
					t.Fatal("preflight accepted disabled or unowned service")
				}
			}
			if err := f.m.start(exe, state, true); err == nil {
				t.Fatal("unsafe job accepted")
			}
			for _, call := range f.calls[before:] {
				if call == "bootout" || call == "bootstrap" || call == "kickstart" || call == "enable" {
					t.Fatal("unowned or disabled service changed")
				}
			}
		})
	}
}
func TestLaunchAgentRemoveOnlyOwnedState(t *testing.T) {
	f := newLaunchFixture(t)
	exe, state := "/test/program/versions/v1/dotcontrol", "/test/state"
	if err := f.m.start(exe, state, true); err != nil {
		t.Fatal(err)
	}
	if err := f.m.remove("/other/state"); err == nil {
		t.Fatal("unowned removal")
	}
	if err := f.m.remove(state); err != nil {
		t.Fatal(err)
	}
	if f.loaded {
		t.Fatal("removed file but left service registered")
	}
}
func TestLaunchAgentRejectsProgramOverrideAndDuplicateKeys(t *testing.T) {
	data := string(jobData("/test/dotcontrol", "/test/state"))
	for _, extra := range []string{"<key>Program</key><string>/other/dotcontrol</string>", "<key>Label</key><string>other</string>"} {
		_, err := parseJob([]byte(strings.Replace(data, "</dict>", extra+"</dict>", 1)))
		if err == nil {
			t.Fatal("ambiguous job accepted")
		}
	}
}
