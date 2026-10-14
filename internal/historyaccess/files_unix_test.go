//go:build !windows

package historyaccess

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUnixStatePermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "history")
	if _, err := NewStore(dir).InitializeAll(); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct {
		path string
		mode os.FileMode
	}{{dir, 0700}, {filepath.Join(dir, "history_access.json"), 0600}, {filepath.Join(dir, "history_access.lock"), 0600}, {filepath.Join(dir, "history_access.initialized"), 0600}} {
		info, err := os.Stat(entry.path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != entry.mode {
			t.Fatalf("wrong mode: %o", info.Mode().Perm())
		}
	}
}
