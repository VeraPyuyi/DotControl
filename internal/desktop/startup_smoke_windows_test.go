//go:build windows

package desktop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeWindowsStartupLifecycle(t *testing.T) {
	if os.Getenv("DOTCONTROL_STARTUP_NATIVE_SMOKE") != "1" {
		t.Skip("opt-in isolated Task Scheduler fixture; production startup is never changed")
	}
	name := fmt.Sprintf("DotControl-Test-%d-%d", os.Getpid(), time.Now().UnixNano())
	ctx := context.Background()
	call := func(operation, xml string, enabled bool) (taskResult, error) {
		return runTaskOperation(ctx, taskRequest{Operation: operation, Name: name, XML: xml, Enabled: enabled})
	}
	initial, e := call("snapshot", "", false)
	if e != nil {
		t.Fatalf("Task Scheduler preflight: %v", e)
	}
	if initial.XML != "" {
		t.Fatal("fixture name collision")
	}
	dir, exe := nativeFixtureExecutable(t)
	task := windowsTaskXML(exe, dir, initial.SID, false)
	def, e := parseTask(task)
	if e != nil {
		t.Fatal(e)
	}
	task = strings.Replace(task, xmlText(def.Actions.Exec[0].Arguments), xmlText("-test.run=^TestNativeLifecycleSupervisor$ -- --dotcontrol-supervisor-fixture "+QuotedWindows(dir)), 1)
	if _, e = call("register", task, true); e != nil {
		t.Fatalf("isolated registration: %v", e)
	}
	t.Cleanup(func() {
		if result, e := call("snapshot", "", false); e == nil {
			t.Logf("isolated task evidence: state=%d last_result=%d running=%v enabled=%v starts=%d", result.State, result.LastResult, result.Running, result.Enabled, fixtureEventCount(dir))
			t.Logf("isolated registered policy: interactive=%v nested_scheduler_retries=%v", strings.Contains(result.XML, "<LogonType>InteractiveToken</LogonType>"), strings.Contains(result.XML, "RestartOnFailure"))
		}
		if _, e := call("disable", "", false); e != nil {
			t.Error(e)
		}
		if _, e := call("remove", "", false); e != nil {
			t.Errorf("isolated task cleanup %s: %v", name, e)
		}
	})
	registered, e := call("snapshot", "", false)
	if e != nil || !strings.Contains(registered.XML, "中文 lifecycle fixture") {
		t.Fatalf("Unicode task roundtrip: %v", e)
	}
	if _, e = call("disable", "", false); e != nil {
		t.Fatal(e)
	}
	disabled, e := call("snapshot", "", false)
	if e != nil || disabled.Enabled {
		t.Fatal("disabled state not retained", e)
	}
	if _, e = call("register", registered.XML, registered.Enabled); e != nil {
		t.Fatal("snapshot restore", e)
	}
	if e = os.WriteFile(filepath.Join(dir, "mode"), []byte("fail-once"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = call("start", "", false); e != nil {
		t.Fatal(e)
	}
	waitFixtureEvents(t, dir, 2, 100*time.Second)
	t.Log("observed OS-managed nonzero exit restart; waiting past restart interval after normal exit")
	time.Sleep(70 * time.Second)
	if got := fixtureEventCount(dir); got != 2 {
		t.Fatalf("normal exit restarted: starts=%d", got)
	}
	latest, e := call("snapshot", "", false)
	if e != nil || latest.Running {
		t.Fatal("fixture still running", e)
	}
	t.Log("isolated Unicode task: registration/restore, failure restart, clean exit verified")
}
