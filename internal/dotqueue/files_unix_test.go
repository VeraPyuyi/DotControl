//go:build !windows

package dotqueue

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUnixPrivacyAndSymlinks(t *testing.T) {
	s, dir := openTestStore(t)
	j := mustSubmit(t, s, "privacy")
	for _, p := range []string{dir, filepath.Join(dir, markerName), filepath.Join(dir, stateFileName), filepath.Join(dir, lockName)} {
		if e := os.Chmod(p, 0777); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := s.Get("client.a", j.ID); e != nil {
		t.Fatal(e)
	}
	for _, p := range []string{dir, filepath.Join(dir, markerName), filepath.Join(dir, stateFileName), filepath.Join(dir, lockName)} {
		i, e := os.Stat(p)
		if e != nil {
			t.Fatal(e)
		}
		want := os.FileMode(0600)
		if p == dir {
			want = 0700
		}
		if i.Mode().Perm() != want {
			t.Error(p, i.Mode())
		}
	}
	marker := filepath.Join(dir, markerName)
	os.Remove(marker)
	target := filepath.Join(t.TempDir(), "marker")
	os.WriteFile(target, []byte("1\n"), 0600)
	if e := os.Symlink(target, marker); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Get("client.a", j.ID); e == nil {
		t.Error("symlink marker accepted")
	}
}
