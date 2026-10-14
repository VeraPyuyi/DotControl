//go:build !windows

package connections

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUnixStatePermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "connections")
	if err := NewStore(dir).SetCodexExecutable(""); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct {
		path string
		mode os.FileMode
	}{{dir, 0700}, {filepath.Join(dir, "connections.json"), 0600}, {filepath.Join(dir, "connections.lock"), 0600}} {
		info, err := os.Stat(entry.path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != entry.mode {
			t.Fatalf("wrong mode: %o", info.Mode().Perm())
		}
	}
}
