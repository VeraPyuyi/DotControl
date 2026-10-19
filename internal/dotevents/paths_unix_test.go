//go:build !windows

package dotevents

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUnixPrivacyEveryTransaction(t *testing.T) {
	m, dir := openTestManager(t)
	paths := []string{dir, filepath.Join(dir, stateFileName), filepath.Join(dir, markerName), filepath.Join(dir, lockName)}
	for _, p := range paths {
		if e := os.Chmod(p, 0777); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := m.Status("alice", "alice"); e != nil {
		t.Fatal(e)
	}
	for i, p := range paths {
		fi, e := os.Stat(p)
		if e != nil {
			t.Fatal(e)
		}
		want := os.FileMode(0600)
		if i == 0 {
			want = 0700
		}
		if fi.Mode().Perm() != want {
			t.Errorf("mode %s %o", p, fi.Mode().Perm())
		}
	}
}
func TestUnixLinkedAncestorOnReopen(t *testing.T) {
	base := t.TempDir()
	target := t.TempDir()
	link := filepath.Join(base, "link")
	if e := os.Symlink(target, link); e != nil {
		t.Fatal(e)
	}
	if _, e := Open(filepath.Join(link, "events"), testKey, nil); e == nil {
		t.Fatal("symlink ancestor accepted")
	}
}
