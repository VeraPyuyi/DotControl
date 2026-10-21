package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/dotcontrol/dotcontrol/internal/config"
)

type unavailableBridgeVault struct{ writes int }

func (v *unavailableBridgeVault) Get(string) (string, error) {
	return "", errors.New("vault unavailable")
}
func (v *unavailableBridgeVault) Set(string, string) error { v.writes++; return nil }
func TestOptionalBridgeUnavailableDoesNotReplaceKey(t *testing.T) {
	v := &unavailableBridgeVault{}
	s, e := openRuntimeDotBridge(context.Background(), physicalTemp(t), config.Config{Listen: "127.0.0.1:9317"}, v, nil, nil)
	if e == nil || s != nil || v.writes != 0 {
		t.Fatalf("service=%v err=%v writes=%d", s, e, v.writes)
	}
}

// physicalTemp resolves the test's temporary root: macOS temporary directories
// sit under the system /var symlink, which the bridge deliberately rejects as
// an ancestor. Production symlink rejection is unchanged.
func physicalTemp(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
