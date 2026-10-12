package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/config"
	"github.com/dotcontrol/dotcontrol/internal/vault"
	"github.com/gofrs/flock"
	"github.com/zalando/go-keyring"
)

func TestUpgradeChecksTargetManifestVersion(t *testing.T) {
	if !validInstalledVersion([]byte("9.9.0\n"), "9.9.0") {
		t.Fatal("cross-version upgrade rejected")
	}
	if validInstalledVersion([]byte("0.2.0-alpha.1\n"), "9.9.0") {
		t.Fatal("old binary accepted as new release")
	}
}

func TestStopLocalChecksIdentityAndWaitsForLifetimeLock(t *testing.T) {
	for _, tc := range []struct {
		name, want                  string
		status, shutdown            int
		mismatch, release, unlocked bool
		wantShutdown                int32
	}{
		{name: "already stopped", unlocked: true},
		{name: "wrong device", mismatch: true, want: "agent_shutdown_identity_unverified"},
		{name: "credentials rejected", status: 401, want: "agent_shutdown_identity_unverified"},
		{name: "shutdown refused", shutdown: 500, wantShutdown: 1, want: "agent_shutdown_refused"},
		{name: "shutdown completed", release: true, wantShutdown: 1},
		{name: "response lost after stop", shutdown: 500, release: true, wantShutdown: 1},
		{name: "accepted but still running", wantShutdown: 1, want: "context deadline exceeded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keyring.MockInit()
			dir := t.TempDir()
			c := config.New("stop test", "unused")
			token := strings.Repeat("t", 32)
			lock := flock.New(filepath.Join(dir, "engine.lock"))
			defer lock.Close()
			if !tc.unlocked {
				if ok, err := lock.TryLock(); err != nil || !ok {
					t.Fatalf("lock: %v", err)
				}
			}
			var shutdowns atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-DotControl-Token") != token {
					t.Error("missing token")
				}
				if r.URL.Path == "/admin/status" {
					if tc.status != 0 {
						http.Error(w, "private response", tc.status)
						return
					}
					id := c.DeviceID
					if tc.mismatch {
						id = "another-device"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"device": map[string]any{"device_id": id}})
					return
				}
				if r.URL.Path != "/admin/shutdown" {
					t.Error("unexpected request")
				}
				shutdowns.Add(1)
				if tc.release {
					if err := lock.Unlock(); err != nil {
						t.Error(err)
					}
				}
				if tc.shutdown != 0 {
					http.Error(w, "private response", tc.shutdown)
				}
			}))
			defer server.Close()
			c.Listen = strings.TrimPrefix(server.URL, "http://")
			if err := config.Save(dir, c); err != nil {
				t.Fatal(err)
			}
			if !tc.unlocked {
				if err := vault.New(dir).Set("admin-token", token); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := stopLocal(ctx, dir)
			got := ""
			if err != nil {
				got = err.Error()
			}
			if got != tc.want {
				t.Fatalf("stop=%q want=%q", got, tc.want)
			}
			if shutdowns.Load() != tc.wantShutdown {
				t.Fatalf("shutdown requests=%d", shutdowns.Load())
			}
			if tc.want == "context deadline exceeded" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("cancellation lost")
			}
			probe := flock.New(filepath.Join(dir, "engine.lock"))
			defer probe.Close()
			ok, err := probe.TryLock()
			if err != nil || ok != (tc.unlocked || tc.release) {
				t.Fatalf("lock safety: acquired=%v err=%v", ok, err)
			}
		})
	}
}

// A legacy agent holds the lifetime lock even with no active desktop action.
// It has an authenticated status endpoint but no shutdown endpoint.
func TestLegacyShutdownIsNotReportedAsActiveTask(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()
	c := config.New("legacy test", "unused")
	token := strings.Repeat("t", 32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-DotControl-Token") != token {
			http.Error(w, "denied", 401)
			return
		}
		if r.URL.Path == "/admin/status" {
			_ = json.NewEncoder(w).Encode(map[string]any{"device": map[string]any{"device_id": c.DeviceID, "version": "0.1.0-dev", "paused": true}})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	c.Listen = strings.TrimPrefix(server.URL, "http://")
	if err := config.Save(dir, c); err != nil {
		t.Fatal(err)
	}
	if err := vault.New(dir).Set("admin-token", token); err != nil {
		t.Fatal(err)
	}
	lock := flock.New(filepath.Join(dir, "engine.lock"))
	if ok, err := lock.TryLock(); err != nil || !ok {
		t.Fatalf("fixture lock: %v", err)
	}
	defer lock.Close()
	before, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	err := stopLocal(ctx, dir)
	if err == nil || err.Error() != "legacy_agent_stop_required" {
		t.Fatalf("idle legacy agent misclassified: %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	if string(before) != string(after) {
		t.Fatal("configuration changed during stop check")
	}
	probe := flock.New(filepath.Join(dir, "engine.lock"))
	defer probe.Close()
	if acquired, _ := probe.TryLock(); acquired {
		t.Fatal("legacy lifetime lock removed or bypassed")
	}
}
