package desktop

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestNativeLifecycleSupervisor(t *testing.T) {
	for i, arg := range os.Args {
		if arg == "--dotcontrol-supervisor-fixture" && i+1 < len(os.Args) {
			dir := os.Args[i+1]
			exe, e := os.Executable()
			if e != nil {
				os.Exit(5)
			}
			if e = OwnChildren(); e != nil {
				os.Exit(5)
			}
			e = superviseChild(context.Background(), dir, func() error {
				cmd := exec.Command(exe, "-test.run=^TestNativeLifecycleHelper$", "--", "--dotcontrol-startup-fixture", dir)
				HideConsole(cmd)
				return cmd.Run()
			}, time.Minute)
			if e != nil {
				os.Exit(7)
			}
			os.Exit(0)
		}
	}
}

// Only OS-launched isolated test copies supply this argument. It never opens
// DotControl configuration, vaults, production task names, or network ports.
func TestNativeLifecycleHelper(t *testing.T) {
	for i, arg := range os.Args {
		if arg == "--dotcontrol-startup-fixture" && i+1 < len(os.Args) {
			dir := os.Args[i+1]
			if runtime.GOOS == "windows" {
				if e := OwnChildren(); e != nil {
					os.Exit(5)
				}
			}
			f, e := os.OpenFile(filepath.Join(dir, "events"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if e != nil {
				os.Exit(5)
			}
			fmt.Fprintln(f, time.Now().UnixNano())
			f.Close()
			mode, _ := os.ReadFile(filepath.Join(dir, "mode"))
			if string(mode) == "fail-once" {
				if e = os.WriteFile(filepath.Join(dir, "mode"), []byte("normal"), 0600); e != nil {
					os.Exit(5)
				}
				os.Exit(7)
			}
			os.Exit(0)
		}
	}
}
func nativeFixtureExecutable(t *testing.T) (string, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "中文 lifecycle fixture")
	if e := os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(exe)
	if e != nil {
		t.Fatal(e)
	}
	copy := filepath.Join(dir, filepath.Base(exe))
	if e = os.WriteFile(copy, b, 0700); e != nil {
		t.Fatal(e)
	}
	return dir, copy
}
func fixtureEventCount(dir string) int {
	b, _ := os.ReadFile(filepath.Join(dir, "events"))
	return len(strings.Fields(string(b)))
}
func waitFixtureEvents(t *testing.T, dir string, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fixtureEventCount(dir) >= want {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("fixture starts=%d, want %d", fixtureEventCount(dir), want)
}
