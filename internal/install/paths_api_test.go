package install

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalPathIsReadOnlyAndRetainsValidation(t *testing.T) {
	base := t.TempDir()
	root, state := filepath.Join(base, "program"), filepath.Join(base, "state")
	gotRoot, e := CanonicalPath(root)
	if e != nil {
		t.Fatal(e)
	}
	gotState, e := CanonicalPath(state)
	if e != nil {
		t.Fatal(e)
	}
	wantRoot, e := canonicalInstallPath(root)
	if e != nil {
		t.Fatal(e)
	}
	wantState, e := canonicalInstallPath(state)
	if e != nil {
		t.Fatal(e)
	}
	if gotRoot != wantRoot || gotState != wantState {
		t.Fatalf("paths=%q %q", gotRoot, gotState)
	}
	for _, p := range []string{root, state} {
		if _, e := os.Stat(p); !os.IsNotExist(e) {
			t.Fatalf("resolution changed filesystem: %v", e)
		}
	}
	if e := ValidatePaths(root, filepath.Join(root, "state")); e == nil {
		t.Fatal("containment accepted")
	}
	if got, e := CanonicalPath(filepath.Join(state, "installer.lock")); e != nil || got != filepath.Join(gotState, "installer.lock") {
		t.Fatalf("lock path=%q %v", got, e)
	}
}
