//go:build darwin

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

func TestNativeDarwinStartupLifecycle(t *testing.T) {
	if os.Getenv("DOTCONTROL_STARTUP_NATIVE_SMOKE") != "1" {
		t.Skip("opt-in isolated launchd fixture; production LaunchAgent is never changed")
	}
	ctx := context.Background()
	if _, e := runLaunchCommand(ctx, "print", launchDomain()); e != nil {
		t.Skip("native launchd fixture unavailable: no interactive GUI domain on this CI runner")
	}
	label := fmt.Sprintf("org.dotcontrol.fixture.%d.%d", os.Getpid(), time.Now().UnixNano())
	service := launchDomain() + "/" + label
	if b, e := runLaunchCommand(ctx, "print", service); e == nil || !launchMissing(b) {
		t.Fatal("fixture collision or discovery unavailable")
	}
	dir, exe := nativeFixtureExecutable(t)
	data := string(launchAgentXML(label, exe, dir))
	start := strings.Index(data, "<key>ProgramArguments</key><array>")
	end := strings.Index(data[start:], "</array>") + start + len("</array>")
	args := "<key>ProgramArguments</key><array>"
	for _, arg := range []string{exe, "-test.run=^TestNativeLifecycleHelper$", "--", "--dotcontrol-startup-fixture", dir} {
		args += "<string>" + xmlText(arg) + "</string>"
	}
	args += "</array>"
	data = data[:start] + args + data[end:]
	p := filepath.Join(dir, "fixture.plist")
	if e := os.WriteFile(p, []byte(data), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "mode"), []byte("fail-once"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := runLaunchCommand(ctx, "bootstrap", launchDomain(), p); e != nil {
		t.Fatalf("isolated bootstrap: %v", e)
	}
	t.Cleanup(func() {
		b, e := runLaunchCommand(ctx, "print", service)
		if e == nil && launchField(string(b), "pid") != "" {
			t.Errorf("fixture still running; did not bootout %s", service)
			return
		}
		if _, e := runLaunchCommand(ctx, "bootout", service); e != nil {
			t.Error("fixture bootout failed", e)
		}
	})
	waitFixtureEvents(t, dir, 2, 100*time.Second)
	t.Log("observed launchd abnormal-exit recovery; waiting past throttle after normal exit")
	time.Sleep(70 * time.Second)
	if got := fixtureEventCount(dir); got != 2 {
		t.Fatalf("normal exit restarted: starts=%d", got)
	}
	t.Log("isolated launchd failure restart and clean exit verified")
}
