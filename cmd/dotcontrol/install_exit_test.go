package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/dotcontrol/dotcontrol/internal/config"
	"github.com/dotcontrol/dotcontrol/internal/desktop"
	"github.com/dotcontrol/dotcontrol/internal/vault"
	"github.com/gofrs/flock"
	"github.com/zalando/go-keyring"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUninstallExitFixture(t *testing.T) {
	for i, arg := range os.Args {
		if arg == "--uninstall-exit-fixture" && i+1 < len(os.Args) {
			dir := os.Args[i+1]
			if err := os.WriteFile(filepath.Join(dir, "ready"), []byte("ready"), 0600); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintln(os.Stdout, "fixture ready")
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				if _, e := os.Stat(filepath.Join(dir, "exit")); e == nil {
					fmt.Fprintln(os.Stdout, "fixture received exit request")
					time.Sleep(500 * time.Millisecond)
					fmt.Fprintln(os.Stdout, "fixture exiting")
					os.Exit(0)
				}
				time.Sleep(10 * time.Millisecond)
			}
			fmt.Fprintln(os.Stderr, "fixture timed out waiting for exit request")
			os.Exit(1)
		}
	}
}
func TestUninstallWaitsForProcessAfterEngineLockRelease(t *testing.T) {
	keyring.MockInit()
	state := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestUninstallExitFixture$", "--", "--uninstall-exit-fixture", state)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	desktop.HideConsole(cmd)
	if e := cmd.Start(); e != nil {
		t.Fatal(e)
	}
	done := make(chan struct{})
	var waitErr error
	go func() {
		waitErr = cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		if err := os.WriteFile(filepath.Join(state, "exit"), []byte("exit"), 0600); err != nil {
			t.Error(err)
		}
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Errorf("fixture %d did not exit during cleanup", cmd.Process.Pid)
			// Only the child created by this test may be terminated.
			if err := cmd.Process.Kill(); err != nil {
				t.Logf("fixture cleanup kill: %v", err)
			}
			<-done
		}
		if waitErr != nil || t.Failed() {
			t.Logf("fixture pid=%d wait=%v output:\n%s", cmd.Process.Pid, waitErr, output.String())
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, e := os.Stat(filepath.Join(state, "ready")); e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	lock := flock.New(filepath.Join(state, "engine.lock"))
	ok, e := lock.TryLock()
	if e != nil || !ok {
		t.Fatal(e)
	}
	defer lock.Close()
	c := config.New("fixture", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/admin/status" {
			json.NewEncoder(w).Encode(map[string]any{"device": map[string]any{"device_id": c.DeviceID}, "runtime": map[string]any{"pid": cmd.Process.Pid}})
			return
		}
		if r.URL.Path == "/admin/shutdown" {
			if err := lock.Unlock(); err != nil {
				t.Error(err)
			}
			if err := os.WriteFile(filepath.Join(state, "exit"), []byte("exit"), 0600); err != nil {
				t.Error(err)
			}
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	c.Listen = strings.TrimPrefix(server.URL, "http://")
	if e = config.Save(state, c); e != nil {
		t.Fatal(e)
	}
	if e = vault.New(state).Set("admin-token", strings.Repeat("t", 32)); e != nil {
		t.Fatal(e)
	}
	start := time.Now()
	if e = stopLocalWithEvidence(context.Background(), state); e != nil {
		t.Fatal(e)
	}
	if time.Since(start) < 500*time.Millisecond {
		t.Fatal("uninstall proceeded at engine release while process was still alive")
	}
	<-done
	if waitErr != nil {
		t.Fatal(waitErr)
	}
}
