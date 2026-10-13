//go:build darwin

package main

import (
	"context"
	"encoding/json"
	"github.com/dotcontrol/dotcontrol/internal/desktop"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDarwinIntegrationAcceptsOnlyKnownSystemAliases(t *testing.T) {
	for _, base := range []string{"", "/tmp", "/var/tmp"} {
		t.Run(base, func(t *testing.T) {
			temp, e := os.MkdirTemp(base, "dotcontrol-integration-alias-")
			if e != nil {
				t.Fatal(e)
			}
			defer os.RemoveAll(temp)
			physical, e := filepath.EvalSymlinks(temp)
			if e != nil {
				t.Fatal(e)
			}
			root, state := filepath.Join(temp, "program"), filepath.Join(temp, "state")
			release, e := lockInstallationState(context.Background(), state)
			if e != nil {
				t.Fatalf("native raw temp lock: %v", e)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			if unlock, e := lockInstallationState(ctx, filepath.Join(physical, "state")); e == nil {
				unlock()
				t.Fatal("physical spelling bypassed state lock")
			}
			release()
			_, _, _, h := activationFixture(t)
			seen := false
			h.checkBinary = func(_ context.Context, exe, _ string) error {
				seen = true
				var snapshot activationSnapshot
				b, e := os.ReadFile(filepath.Join(root, "activation-recovery.json"))
				if e != nil {
					t.Fatal(e)
				}
				if json.Unmarshal(b, &snapshot) != nil || snapshot.State != state || !strings.HasPrefix(exe, filepath.Join(physical, "program")+string(filepath.Separator)) {
					t.Fatalf("state namespace changed or executable uncanonical: %+v %s", snapshot, exe)
				}
				return nil
			}
			if _, e := applyInstallation(context.Background(), installationRequest{State: state, Root: root, Bundle: activationBundle(t, "1.0"), NoStart: true}, h); e != nil {
				t.Fatal(e)
			}
			if !seen {
				t.Fatal("health fixture not reached")
			}
			startup := desktop.StartupSnapshot{Exists: true, Data: []byte(`<plist><dict><key>ProgramArguments</key><array><string>agent</string><string>--state</string><string>` + state + `</string><string>serve</string></array></dict></plist>`)}
			if !startupBelongsToState(startup, state) {
				t.Fatal("existing original-spelling startup rejected")
			}
			custom := filepath.Join(temp, "custom-link")
			if e = os.Symlink(filepath.Join(physical, "state"), custom); e != nil {
				t.Fatal(e)
			}
			if unlock, e := lockInstallationState(context.Background(), custom); e == nil {
				unlock()
				t.Fatal("arbitrary symlink lock accepted")
			}
		})
	}
}
