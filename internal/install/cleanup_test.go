package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func enableCleanup(t *testing.T, o *Options, guard func(context.Context, Current) error) {
	t.Helper()
	o.CleanupOldVersions = true
	o.BeforeCleanup = guard
}

func TestCleanupOnlyAfterHealthPreservesUnknownFiles(t *testing.T) {
	o := opts(t, bundle(t, "0.2.0", "old"))
	old, e := Apply(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	unknown := filepath.Join(old.Current.Dir, "bin", "notes.txt")
	if e = os.WriteFile(unknown, []byte("keep"), 0600); e != nil {
		t.Fatal(e)
	}
	o.BundleDir = bundle(t, "0.2.1", "new")
	var healthy, guarded bool
	var phases []string
	o.Progress = func(p Progress) { phases = append(phases, p.Phase) }
	o.Health = func(_ context.Context, target Current) error {
		if _, e := os.Stat(filepath.Join(old.Current.Dir, "bin", "dotcontrol")); e != nil {
			t.Fatalf("old payload removed before health: %v", e)
		}
		healthy = true
		return nil
	}
	enableCleanup(t, &o, func(_ context.Context, candidate Current) error {
		if !healthy || candidate != old.Current {
			t.Fatalf("bad cleanup order/target: %#v", candidate)
		}
		p, e := ReadProgress(o.Root)
		if e != nil || p.Phase != "cleaning_old_versions" || p.RecoveryNeeded {
			t.Fatalf("cleanup lacks durable health commit: %#v %v", p, e)
		}
		guarded = true
		return nil
	})
	next, e := Apply(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	if !guarded {
		t.Fatal("cleanup guard was not called")
	}
	if _, e = os.Stat(filepath.Join(old.Current.Dir, "bin", "dotcontrol")); !os.IsNotExist(e) {
		t.Fatalf("old payload remains: %v", e)
	}
	if _, e = os.Stat(filepath.Join(old.Current.Dir, "manifest.json")); !os.IsNotExist(e) {
		t.Fatalf("old manifest remains: %v", e)
	}
	if b, e := os.ReadFile(unknown); e != nil || string(b) != "keep" {
		t.Fatalf("unknown file changed: %q %v", b, e)
	}
	active, e := ReadCurrent(o.Root)
	if e != nil || active != next.Current {
		t.Fatalf("new pointer changed: %#v %v", active, e)
	}
	if strings.Join(phases, ",") != "download,verify,stage,install,waiting_for_exit,health,cleaning_old_versions,complete" {
		t.Fatal(phases)
	}
	own, e := loadOwnership(o.Root)
	if e != nil || len(own.Versions) != 1 || own.Versions[0].Dir != filepath.Base(next.Current.Dir) {
		t.Fatalf("ownership not retired: %#v %v", own, e)
	}
}

func TestCleanupFailedHealthRetainsOldPayload(t *testing.T) {
	o := opts(t, bundle(t, "0.2.0", "old"))
	old, e := Apply(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	o.BundleDir = bundle(t, "0.2.1", "new")
	o.Health = func(context.Context, Current) error { return errors.New("unhealthy") }
	enableCleanup(t, &o, func(context.Context, Current) error { t.Fatal("cleanup before healthy"); return nil })
	if _, e = Apply(context.Background(), o); e == nil {
		t.Fatal("accepted failed health")
	}
	if _, e = Verify(context.Background(), old.Current.Dir); e != nil {
		t.Fatalf("old payload changed: %v", e)
	}
	active, e := ReadCurrent(o.Root)
	if e != nil || active != old.Current {
		t.Fatalf("rollback failed: %#v %v", active, e)
	}
}

func assertCleanupPending(t *testing.T, o Options, r Result, err error) {
	t.Helper()
	if !errors.Is(err, ErrCleanupPending) || !r.CleanupPending || r.Current.Version != "0.2.1" {
		t.Fatalf("expected committed cleanup_pending: %#v %v", r, err)
	}
	active, e := ReadCurrent(o.Root)
	if e != nil || active != r.Current {
		t.Fatalf("new current lost: %#v %v", active, e)
	}
	p, e := ReadProgress(o.Root)
	if e != nil || p.Phase != "cleanup_pending" || p.RecoveryNeeded || p.Error == "" {
		t.Fatalf("bad cleanup journal: %#v %v", p, e)
	}
}

func TestCleanupGuardRefusesReferencedGenerationAndRetries(t *testing.T) {
	for _, retry := range []string{"apply", "repair"} {
		t.Run(retry, func(t *testing.T) {
			o := opts(t, bundle(t, "0.2.0", "old"))
			old, e := Apply(context.Background(), o)
			if e != nil {
				t.Fatal(e)
			}
			o.BundleDir = bundle(t, "0.2.1", "new")
			o.Rollback = func(context.Context, *Current) error { t.Fatal("rolled back committed health"); return nil }
			enableCleanup(t, &o, func(context.Context, Current) error { return errors.New("generation referenced by live launcher") })
			r, e := Apply(context.Background(), o)
			assertCleanupPending(t, o, r, e)
			if _, e = Verify(context.Background(), old.Current.Dir); e != nil {
				t.Fatal(e)
			}
			o.BundleDir = ""
			o.BeforeActivate = func(context.Context, Current) error { t.Fatal("recovery should only clean"); return nil }
			o.BeforeCleanup = func(context.Context, Current) error { return nil }
			var next Result
			if retry == "apply" {
				next, e = Apply(context.Background(), o)
			} else {
				next, e = Repair(context.Background(), o)
			}
			if e != nil || next.CleanupPending || next.Current != r.Current {
				t.Fatalf("retry: %#v %v", next, e)
			}
			if _, e = os.Stat(old.Current.Dir); !os.IsNotExist(e) {
				t.Fatalf("old generation remains: %v", e)
			}
		})
	}
}

func TestCleanupRequiresGuardAndRetainsCurrentGeneration(t *testing.T) {
	o := opts(t, bundle(t, "0.2.0", "old"))
	if _, e := Apply(context.Background(), o); e != nil {
		t.Fatal(e)
	}
	o.BundleDir = bundle(t, "0.2.1", "new")
	o.CleanupOldVersions = true
	r, e := Apply(context.Background(), o)
	assertCleanupPending(t, o, r, e)
	o.BeforeCleanup = func(_ context.Context, candidate Current) error {
		if candidate == r.Current {
			t.Fatal("attempted cleanup of current")
		}
		return nil
	}
	if _, e = Apply(context.Background(), o); e != nil {
		t.Fatal(e)
	}
	if _, e = Verify(context.Background(), r.Current.Dir); e != nil {
		t.Fatal(e)
	}
}

func TestCleanupPartialDeletionPreservesModifiedFileAndRetries(t *testing.T) {
	b := bundle(t, "0.2.0", "old")
	m, e := Verify(context.Background(), b)
	if e != nil {
		t.Fatal(e)
	}
	data := []byte("original")
	h := sha256.Sum256(data)
	m.Files = append(m.Files, File{Path: "z-owned.txt", SHA256: hex.EncodeToString(h[:]), Mode: 0600})
	if e = os.WriteFile(filepath.Join(b, "z-owned.txt"), data, 0600); e != nil {
		t.Fatal(e)
	}
	writeManifest(t, b, m)
	o := opts(t, b)
	old, e := Apply(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	changed := filepath.Join(old.Current.Dir, "z-owned.txt")
	if e = os.WriteFile(changed, []byte("user modification"), 0600); e != nil {
		t.Fatal(e)
	}
	o.BundleDir = bundle(t, "0.2.1", "new")
	o.Rollback = func(context.Context, *Current) error {
		t.Fatal("rolled back into partially deleted generation")
		return nil
	}
	enableCleanup(t, &o, func(context.Context, Current) error { return nil })
	r, e := Apply(context.Background(), o)
	assertCleanupPending(t, o, r, e)
	if _, e = os.Stat(filepath.Join(old.Current.Dir, "bin", "dotcontrol")); !os.IsNotExist(e) {
		t.Fatalf("fixture did not partially delete: %v", e)
	}
	if b, e := os.ReadFile(changed); e != nil || string(b) != "user modification" {
		t.Fatalf("modified file lost: %q %v", b, e)
	}
	// The user resolves the changed file by moving it out of the owned path.
	if e = os.Rename(changed, filepath.Join(old.Current.Dir, "saved-user-copy")); e != nil {
		t.Fatal(e)
	}
	o.BundleDir = ""
	next, e := Repair(context.Background(), o)
	if e != nil || next.Current != r.Current {
		t.Fatalf("partial retry: %#v %v", next, e)
	}
	if _, e = os.Stat(filepath.Join(old.Current.Dir, "manifest.json")); !os.IsNotExist(e) {
		t.Fatal("manifest remains", e)
	}
}

func TestCleanupCancellationAfterHealthNeverRollsBack(t *testing.T) {
	for _, phase := range []string{"cleaning_old_versions", "complete"} {
		t.Run(phase, func(t *testing.T) {
			o := opts(t, bundle(t, "0.2.0", "old"))
			if _, e := Apply(context.Background(), o); e != nil {
				t.Fatal(e)
			}
			o.BundleDir = bundle(t, "0.2.1", "new")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			o.Progress = func(p Progress) {
				if p.Phase == phase {
					cancel()
				}
			}
			o.Rollback = func(context.Context, *Current) error { t.Fatal("rolled back after health commit"); return nil }
			enableCleanup(t, &o, func(context.Context, Current) error { return nil })
			r, e := Apply(ctx, o)
			assertCleanupPending(t, o, r, e)
			if !errors.Is(e, context.Canceled) {
				t.Fatal(e)
			}
			o.Progress, o.BundleDir = nil, ""
			if _, e = Apply(context.Background(), o); e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestCleanupRejectsUnsafeOwnedPaths(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "directory", "manifest_changed", "traversal"} {
		t.Run(kind, func(t *testing.T) {
			o := opts(t, bundle(t, "0.2.0", "old"))
			old, e := Apply(context.Background(), o)
			if e != nil {
				t.Fatal(e)
			}
			outside := filepath.Join(t.TempDir(), "outside")
			if e = os.WriteFile(outside, []byte("keep"), 0600); e != nil {
				t.Fatal(e)
			}
			p := filepath.Join(old.Current.Dir, "bin", "dotcontrol")
			switch kind {
			case "symlink", "directory":
				if e = os.Remove(p); e != nil {
					t.Fatal(e)
				}
				if kind == "symlink" {
					e = os.Symlink(outside, p)
				} else {
					e = os.Mkdir(p, 0700)
				}
				if e != nil {
					t.Skip(e)
				}
			case "hardlink":
				e = os.Link(p, filepath.Join(t.TempDir(), "linked"))
				if e != nil {
					t.Skip(e)
				}
			case "manifest_changed":
				if e = os.WriteFile(filepath.Join(old.Current.Dir, "manifest.json"), []byte("user changed manifest"), 0600); e != nil {
					t.Fatal(e)
				}
			case "traversal":
				own, e := loadOwnership(o.Root)
				if e != nil {
					t.Fatal(e)
				}
				own.Versions[0].Manifest.Files[0].Path = "../outside"
				own.Versions[0].Hash = manifestHash(own.Versions[0].Manifest)
				if e = atomicJSON(filepath.Join(o.Root, ownerName), own); e != nil {
					t.Fatal(e)
				}
			}
			o.BundleDir = bundle(t, "0.2.1", "new")
			enableCleanup(t, &o, func(context.Context, Current) error { return nil })
			if _, e = Apply(context.Background(), o); e == nil {
				t.Fatal("unsafe cleanup accepted")
			}
			if b, e := os.ReadFile(outside); e != nil || string(b) != "keep" {
				t.Fatalf("outside file changed: %q %v", b, e)
			}
		})
	}
}

func TestCleanupRecoversAfterProcessExit(t *testing.T) {
	if root := os.Getenv("DOTCONTROL_CLEANUP_TEST_ROOT"); root != "" {
		o := Options{Root: root, StateDir: root + "-state", BundleDir: os.Getenv("DOTCONTROL_CLEANUP_TEST_BUNDLE"), CleanupOldVersions: true}
		o.BeforeCleanup = func(_ context.Context, candidate Current) error {
			if candidate.Version == "0.2.0" {
				os.Exit(29)
			}
			return nil
		}
		_, _ = Apply(context.Background(), o)
		os.Exit(30)
	}
	o := opts(t, bundle(t, "0.1.9", "oldest"))
	oldest, e := Apply(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	o.BundleDir = bundle(t, "0.2.0", "old")
	old, e := Apply(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	o.BundleDir = bundle(t, "0.2.1", "new")
	cmd := exec.Command(os.Args[0], "-test.run=^TestCleanupRecoversAfterProcessExit$")
	cmd.Env = append(os.Environ(), "DOTCONTROL_CLEANUP_TEST_ROOT="+o.Root, "DOTCONTROL_CLEANUP_TEST_BUNDLE="+o.BundleDir)
	output, e := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(e, &exit) || exit.ExitCode() != 29 {
		t.Fatalf("child did not terminate during cleanup: %s %v", output, e)
	}
	if _, e = os.Stat(oldest.Current.Dir); !os.IsNotExist(e) {
		t.Fatal("oldest was not cleaned before exit", e)
	}
	if _, e = Verify(context.Background(), old.Current.Dir); e != nil {
		t.Fatal(e)
	}
	current, e := ReadCurrent(o.Root)
	if e != nil || current.Version != "0.2.1" {
		t.Fatalf("new current lost: %#v %v", current, e)
	}
	o.BundleDir = ""
	o.Rollback = func(context.Context, *Current) error { t.Fatal("rollback after crash in cleanup"); return nil }
	enableCleanup(t, &o, func(context.Context, Current) error { return nil })
	r, e := Repair(context.Background(), o)
	if e != nil || r.Current != current {
		t.Fatalf("restart recovery: %#v %v", r, e)
	}
	if _, e = os.Stat(old.Current.Dir); !os.IsNotExist(e) {
		t.Fatal("old was not cleaned on restart", e)
	}
}

func TestDownloadPreservesPendingCleanupJournal(t *testing.T) {
	o := opts(t, bundle(t, "0.2.0", "old"))
	if _, e := Apply(context.Background(), o); e != nil {
		t.Fatal(e)
	}
	o.BundleDir = bundle(t, "0.2.1", "new")
	enableCleanup(t, &o, func(context.Context, Current) error { return errors.New("referenced") })
	r, e := Apply(context.Background(), o)
	assertCleanupPending(t, o, r, e)
	body := []byte("archive")
	h := sha256.Sum256(body)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
	defer server.Close()
	if _, e = Download(context.Background(), DownloadOptions{Root: o.Root, StateDir: o.StateDir, URL: server.URL, SHA256: hex.EncodeToString(h[:]), Client: server.Client()}); e != nil {
		t.Fatal(e)
	}
	p, e := ReadProgress(o.Root)
	if e != nil || p.Phase != "cleanup_pending" || p.Target == nil || *p.Target != r.Current {
		t.Fatalf("download erased cleanup recovery: %#v %v", p, e)
	}
}
