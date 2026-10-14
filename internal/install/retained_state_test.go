package install

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnprovenUninstallCannotMintStateBinding(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty-root", true: "failed-install"}[failed], func(t *testing.T) {
			o := opts(t, bundle(t, "1.0", "payload"))
			if err := os.MkdirAll(o.Root, 0700); err != nil {
				t.Fatal(err)
			}
			if failed {
				o.Health = func(context.Context, Current) error { return errors.New("health failed") }
				if _, err := Apply(context.Background(), o); err == nil {
					t.Fatal("expected failure")
				}
			}
			if err := Remove(context.Background(), RemoveOptions{Root: o.Root, StateDir: o.StateDir}); err != nil {
				t.Fatal(err)
			}
			if ok, err := RetainedStateMatches(o.Root, o.StateDir); err != nil || ok {
				t.Fatalf("minted receipt: %v %v", ok, err)
			}
		})
	}
}

func TestManagedStateBindingSurvivesCleanupAndRejectsRebinding(t *testing.T) {
	o := opts(t, bundle(t, "1.0", "one"))
	o.Health = func(context.Context, Current) error { return nil }
	if _, err := Apply(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	o.CleanupOldVersions = true
	o.BeforeCleanup = func(context.Context, Current) error { return nil }
	o.BundleDir = bundle(t, "1.1", "two")
	if _, err := Apply(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	for _, uninstalled := range []bool{false, true} {
		if uninstalled {
			if err := Remove(context.Background(), RemoveOptions{Root: o.Root, StateDir: o.StateDir}); err != nil {
				t.Fatal(err)
			}
		}
		before, err := os.ReadFile(filepath.Join(o.Root, ownerName))
		if err != nil {
			t.Fatal(err)
		}
		other := filepath.Join(filepath.Dir(o.StateDir), "unrelated state")
		err = Remove(context.Background(), RemoveOptions{Root: o.Root, StateDir: other, BeforeRemove: func(context.Context) error { t.Fatal("foreign stop invoked"); return nil }})
		if err == nil || !strings.Contains(err.Error(), "install_state_binding_mismatch") {
			t.Fatal(err)
		}
		after, _ := os.ReadFile(filepath.Join(o.Root, ownerName))
		if string(before) != string(after) {
			t.Fatal("binding changed")
		}
	}
	if ok, err := RetainedStateMatches(o.Root, o.StateDir); err != nil || !ok {
		t.Fatalf("lost receipt: %v %v", ok, err)
	}
}

func TestOwnershipDoesNotCaseFoldDistinctPaths(t *testing.T) {
	base := t.TempDir()
	a, b := filepath.Join(base, "State"), filepath.Join(base, "state")
	for _, p := range []string{a, b} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	ai, _ := os.Stat(a)
	bi, _ := os.Stat(b)
	if os.SameFile(ai, bi) {
		t.Skip("filesystem treats case aliases as the same directory")
	}
	if sameManagedPath(a, b) {
		t.Fatal("distinct paths share ownership")
	}
	o := opts(t, bundle(t, "vA", "one"))
	o.StateDir = a
	installed, err := Apply(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	current := installed.Current
	current.Dir = filepath.Join(filepath.Dir(current.Dir), strings.ToLower(filepath.Base(current.Dir)))
	if err := os.MkdirAll(current.Dir, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := OwnedFile(o.Root, current, "bin/dotcontrol"); err == nil {
		t.Fatal("foreign case variant treated as owned")
	}
	if err := Remove(context.Background(), RemoveOptions{Root: o.Root, StateDir: a}); err != nil {
		t.Fatal(err)
	}
	if ok, err := RetainedStateMatches(o.Root, b); err != nil || ok {
		t.Fatalf("case variant authorized: %v %v", ok, err)
	}
}

func TestRetainedReceiptSurvivesActivationBeforeCommitRecovery(t *testing.T) {
	o := opts(t, bundle(t, "1.0", "payload"))
	o.Health = func(context.Context, Current) error { return nil }
	if _, err := Apply(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if err := Remove(context.Background(), RemoveOptions{Root: o.Root, StateDir: o.StateDir}); err != nil {
		t.Fatal(err)
	}
	r, err := Apply(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	// Reconstruct the durable files at the crash point after health and ownership
	// save, before a commit phase is saved. No prior current existed on reinstall.
	if err := atomicJSON(filepath.Join(o.Root, "transaction.json"), Progress{Phase: "health", Target: &r.Current, RecoveryNeeded: true}); err != nil {
		t.Fatal(err)
	}
	o.BeforeActivate = func(context.Context, Current) error {
		if _, err := ReadCurrent(o.Root); !os.IsNotExist(err) {
			t.Fatal("recovery did not remove uncommitted pointer", err)
		}
		if ok, err := RetainedStateMatches(o.Root, o.StateDir); err != nil || !ok {
			t.Fatal("lost reinstall provenance", err)
		}
		return nil
	}
	if _, err := Apply(context.Background(), o); err != nil {
		t.Fatal(err)
	}
}

func TestOwnedFileRejectsRecordedSymlinkAlias(t *testing.T) {
	o := opts(t, bundle(t, "1.0", "payload"))
	r, err := Apply(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	other := r.Current
	other.Dir = r.Current.Dir + "-other"
	if err := os.Rename(r.Current.Dir, other.Dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other.Dir, r.Current.Dir); err != nil {
		t.Skip("symlink unavailable", err)
	}
	if _, err := OwnedFile(o.Root, other, "bin/dotcontrol"); err == nil {
		t.Fatal("symlink minted ownership for unrecorded directory")
	}
}

func TestReinstallPreservesBindingSpelling(t *testing.T) {
	o := opts(t, bundle(t, "1.0", "payload"))
	o.StateDir = filepath.Join(filepath.Dir(o.StateDir), "State")
	if err := os.MkdirAll(o.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	alternate := filepath.Join(filepath.Dir(o.StateDir), "state")
	ai, _ := os.Stat(o.StateDir)
	bi, err := os.Stat(alternate)
	if err != nil || !os.SameFile(ai, bi) {
		t.Skip("filesystem is case sensitive")
	}
	if _, err := Apply(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if err := Remove(context.Background(), RemoveOptions{Root: o.Root, StateDir: o.StateDir}); err != nil {
		t.Fatal(err)
	}
	own, err := loadOwnership(o.Root)
	if err != nil {
		t.Fatal(err)
	}
	o.StateDir = alternate
	if _, err := Apply(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	after, err := loadOwnership(o.Root)
	if err != nil || after.StateDir != own.StateDir || after.RetainedState != own.RetainedState {
		t.Fatal("changed binding spelling", err)
	}
}
