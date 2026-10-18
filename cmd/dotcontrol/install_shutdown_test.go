package main

import (
	"context"
	"encoding/json"
	"github.com/dotcontrol/dotcontrol/internal/config"
	"github.com/dotcontrol/dotcontrol/internal/vault"
	"github.com/gofrs/flock"
	"github.com/zalando/go-keyring"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpgradeShutdownNeverFallsBackToTaskCancellation(t *testing.T) {
	for _, code := range []int{http.StatusNotFound, http.StatusConflict} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			keyring.MockInit()
			state := t.TempDir()
			c := config.New("test", "")
			requests := []string{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.URL.Path)
				if r.URL.Path == "/admin/status" {
					json.NewEncoder(w).Encode(map[string]any{"device": map[string]string{"device_id": c.DeviceID}})
					return
				}
				http.Error(w, "refused", code)
			}))
			defer server.Close()
			c.Listen = strings.TrimPrefix(server.URL, "http://")
			config.Save(state, c)
			vault.New(state).Set("admin-token", strings.Repeat("t", 32))
			lock := flock.New(filepath.Join(state, "engine.lock"))
			ok, e := lock.TryLock()
			if e != nil || !ok {
				t.Fatal(e)
			}
			defer lock.Close()
			err := stopForUpgrade(context.Background(), state)
			if err == nil {
				t.Fatal("refusal accepted")
			}
			if len(requests) != 2 || requests[1] != "/admin/upgrade/shutdown" {
				t.Fatalf("unsafe shutdown path: %v", requests)
			}
			if code == 404 && err.Error() != "legacy_agent_stop_required" {
				t.Fatal(err)
			}
		})
	}
}
