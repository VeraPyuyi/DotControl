//go:build windows

package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func mockWindowsStartup(t *testing.T) (*StartupSnapshot, *taskResult, *[]string) {
	t.Helper()
	oldTask, oldRead, oldWrite := taskOperation, readRunStartup, writeRunStartup
	oldParent := managedParentCheck
	managedParentCheck = func(string) bool { return true }
	oldStatus := CachedStartupStatus()
	t.Cleanup(func() {
		taskOperation, readRunStartup, writeRunStartup = oldTask, oldRead, oldWrite
		managedParentCheck = oldParent
		cacheStartupStatus(oldStatus)
	})
	run := &StartupSnapshot{}
	task := &taskResult{SID: "S-1-5-21-fixture"}
	events := &[]string{}
	taskOperation = func(_ context.Context, r taskRequest) (taskResult, error) {
		*events = append(*events, r.Operation)
		switch r.Operation {
		case "register":
			task.XML = r.XML
			task.Enabled = r.Enabled
		case "disable":
			task.Enabled = false
		case "start":
			if !task.Enabled {
				return taskResult{}, errors.New("disabled")
			}
			task.Running = true
		case "remove":
			task.XML = ""
			task.Enabled = false
		}
		return *task, nil
	}
	readRunStartup = func() (StartupSnapshot, error) { return *run, nil }
	writeRunStartup = func(s StartupSnapshot) error {
		*events = append(*events, "write-run")
		*run = StartupSnapshot{Exists: s.Exists, Value: s.Value, Kind: s.Kind}
		return nil
	}
	return run, task, events
}
func TestWindowsStartupMigrationSuspensionAndExactRollback(t *testing.T) {
	run, task, events := mockWindowsStartup(t)
	state := t.TempDir()
	exe := filepath.Join(t.TempDir(), "中文 agent.exe")
	run.Exists = true
	run.Value = QuotedWindows(exe) + " --state " + QuotedWindows(state) + " serve"
	run.Kind = 2
	task.XML = windowsTaskXML(exe, state, "S-1-5-21-fixture", false)
	task.Enabled = false
	original, e := SnapshotStartup()
	if e != nil {
		t.Fatal(e)
	}
	if e = ConfigureStartup(exe, state, true); e != nil {
		t.Fatal(e)
	}
	if run.Exists || !task.Enabled {
		t.Fatal("Run migration failed")
	}
	if e = StartManaged(exe, state); e != nil {
		t.Fatal(e)
	}
	*events = nil
	if e = SuspendStartup(state); e != nil {
		t.Fatal(e)
	}
	if suspended, e := RuntimeSuspended(state); e != nil || !suspended || task.Enabled {
		t.Fatal("suspension failed")
	}
	if !reflect.DeepEqual(*events, []string{"snapshot", "disable", "write-run"}) {
		t.Fatal(*events)
	}
	if e = RestoreStartup(original); e != nil {
		t.Fatal(e)
	}
	restored, e := SnapshotStartup()
	if e != nil {
		t.Fatal(e)
	}
	restored.TaskRunning = original.TaskRunning
	if !reflect.DeepEqual(restored, original) {
		t.Fatalf("snapshot changed: %+v", restored)
	}
	if e = ResumeStartup(state); e != nil {
		t.Fatal(e)
	}
}
func TestWindowsStartupRejectsForeignTaskAndCachesSanitizedManualStatus(t *testing.T) {
	_, task, events := mockWindowsStartup(t)
	state := t.TempDir()
	exe := filepath.Join(t.TempDir(), "agent.exe")
	task.XML = windowsTaskXML(exe, "foreign-state", "sid", true)
	task.Enabled = true
	if e := ConfigureStartup(exe, state, true); e == nil {
		t.Fatal("foreign task replaced")
	}
	for _, v := range *events {
		if v != "snapshot" {
			t.Fatal("foreign task mutated", *events)
		}
	}
	task.XML = windowsTaskXML(exe, state, "sid", false)
	task.Running = true
	RefreshStartupStatus(exe, state, "")
	if CachedStartupStatus().Managed {
		t.Fatal("manual start claimed OS supervision")
	}
	RefreshStartupStatus(exe, state, "task-scheduler")
	s := CachedStartupStatus()
	if !s.Managed || s.Configured {
		t.Fatal(s)
	}
	n := len(*events)
	for range 100 {
		_ = CachedStartupStatus()
		_ = AutostartEnabled()
	}
	if len(*events) != n {
		t.Fatal("status polling invoked OS")
	}
	b, _ := json.Marshal(s)
	if strings.Contains(string(b), state) || strings.Contains(string(b), exe) {
		t.Fatal("status leaked paths")
	}
	taskOperation = func(context.Context, taskRequest) (taskResult, error) {
		return taskResult{}, errors.New("private path")
	}
	RefreshStartupStatus(exe, state, "task-scheduler")
	if s = CachedStartupStatus(); s.Managed || s.Reason != "startup_status_unavailable" {
		t.Fatal(s)
	}
}
func TestTaskScriptUsesUnicodeDataAndNoTerminationOrElevation(t *testing.T) {
	for _, required := range []string{"InputEncoding", "OutputEncoding", "ReadToEnd() | ConvertFrom-Json", "$t.Enabled=$false", "$t.Run($null)"} {
		if !strings.Contains(taskScript, required) {
			t.Fatal(required)
		}
	}
	for _, forbidden := range []string{".Stop(", "Stop-Process", "/F", "HighestAvailable", "Password"} {
		if strings.Contains(taskScript, forbidden) {
			t.Fatal(forbidden)
		}
	}
}
func TestSuspendedRuntimeInterlockIsDistinctFromEngineLock(t *testing.T) {
	state := t.TempDir()
	lock := filepath.Join(state, "engine.lock")
	os.WriteFile(lock, []byte("retained"), 0600)
	if e := suspendRuntime(state); e != nil {
		t.Fatal(e)
	}
	if e := ResumeStartup(state); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(lock)
	if e != nil || string(b) != "retained" {
		t.Fatal("engine lock altered")
	}
}
