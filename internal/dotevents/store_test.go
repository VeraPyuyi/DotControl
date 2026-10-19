package dotevents

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestProcessLockHelper(t *testing.T) {
	if os.Getenv("DOT_EVENTS_TEST_HELPER") != "1" {
		return
	}
	m, e := Open(os.Getenv("DOT_EVENTS_TEST_DIR"), testKey, nil)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 12; i++ {
		if e = m.SetEnabled(i%2 == 0); e != nil {
			t.Fatal(e)
		}
	}
}
func TestCrossProcessLockAndMissingState(t *testing.T) {
	m, dir := openTestManager(t)
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	cmds := make([]*exec.Cmd, 3)
	outputs := make([]bytes.Buffer, len(cmds))
	for i := range cmds {
		c := exec.Command(exe, "-test.run=^TestProcessLockHelper$")
		c.Stdout = &outputs[i]
		c.Stderr = &outputs[i]
		c.Env = append(os.Environ(), "DOT_EVENTS_TEST_HELPER=1", "DOT_EVENTS_TEST_DIR="+dir)
		cmds[i] = c
		if e = c.Start(); e != nil {
			t.Fatal(e)
		}
	}
	for i, c := range cmds {
		if e = c.Wait(); e != nil {
			t.Errorf("child %d: %v\n%s", i, e, outputs[i].String())
		}
	}
	if t.Failed() {
		t.FailNow()
	}
	if _, e = m.Status("alice", "alice"); e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(filepath.Join(dir, stateFileName)); e != nil {
		t.Fatal(e)
	}
	if _, e = m.Status("alice", "alice"); e == nil {
		t.Fatal("missing state reset")
	}
}
func TestRejectHardlinkState(t *testing.T) {
	m, dir := openTestManager(t)
	if e := os.Link(filepath.Join(dir, stateFileName), filepath.Join(dir, "alias")); e != nil {
		t.Skip(e)
	}
	if _, e := m.Status("alice", "alice"); e == nil {
		t.Fatal("hardlinked state accepted")
	}
}
