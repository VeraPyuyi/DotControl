package main

import (
	"context"
	"encoding/json"
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
			os.WriteFile(filepath.Join(dir, "ready"), []byte("ready"), 0600)
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				if _, e := os.Stat(filepath.Join(dir, "exit")); e == nil {
					time.Sleep(500 * time.Millisecond)
					os.Exit(0)
				}
				time.Sleep(10 * time.Millisecond)
			}
			os.Exit(1)
		}
	}
}
func TestUninstallWaitsForProcessAfterEngineLockRelease(t *testing.T) {
	keyring.MockInit()
	state := t.TempDir()
	exe, _ := os.Executable()
	cmd := exec.Command(exe, "-test.run=^TestUninstallExitFixture$", "--", "--uninstall-exit-fixture", state)
	desktop.HideConsole(cmd)
	if e := cmd.Start(); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer os.WriteFile(filepath.Join(state, "exit"), []byte("exit"), 0600)
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
			lock.Unlock()
			os.WriteFile(filepath.Join(state, "exit"), []byte("exit"), 0600)
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
	if e = <-done; e != nil {
		t.Fatal(e)
	}
}
