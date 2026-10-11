package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func bundle(t *testing.T, version, body string) string {
	t.Helper()
	d := t.TempDir()
	if err := os.MkdirAll(filepath.Join(d, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "bin", "dotcontrol"), []byte(body), 0755); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256([]byte(body))
	m := Manifest{Version: version, OS: runtime.GOOS, Arch: runtime.GOARCH, Files: []File{{Path: "bin/dotcontrol", SHA256: hex.EncodeToString(h[:]), Mode: 0755}}}
	writeManifest(t, d, m)
	return d
}
func writeManifest(t *testing.T, d string, m Manifest) {
	t.Helper()
	b, e := json.Marshal(m)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(d, "manifest.json"), b, 0600); e != nil {
		t.Fatal(e)
	}
}
func opts(t *testing.T, b string) Options {
	t.Helper()
	base := t.TempDir()
	return Options{Root: filepath.Join(base, "程序 安装"), StateDir: filepath.Join(base, "数据 state"), BundleDir: b}
}

func TestApplyAndHealthRollback(t *testing.T) {
	o := opts(t, bundle(t, "0.2.0", "one"))
	var phases []string
	o.Progress = func(p Progress) { phases = append(phases, p.Phase) }
	first, e := Apply(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	if first.Current.Version != "0.2.0" {
		t.Fatal(first)
	}
	data, e := os.ReadFile(filepath.Join(first.Current.Dir, "bin", "dotcontrol"))
	if e != nil || string(data) != "one" {
		t.Fatalf("bytes=%q err=%v", data, e)
	}
	if strings.Join(phases, ",") != "download,verify,stage,install,health,complete" {
		t.Fatal(phases)
	}
	o.BundleDir = bundle(t, "0.2.1", "two")
	o.Health = func(_ context.Context, c Current) error {
		active, e := ReadCurrent(o.Root)
		if e != nil || active.Version != c.Version {
			t.Fatalf("health not active: %v", e)
		}
		return errors.New("unhealthy")
	}
	if _, e = Apply(context.Background(), o); e == nil {
		t.Fatal("accepted unhealthy release")
	}
	active, e := ReadCurrent(o.Root)
	if e != nil || active != first.Current {
		t.Fatalf("rollback: %#v %v", active, e)
	}
	p, e := ReadProgress(o.Root)
	if e != nil || p.Phase != "failed" {
		t.Fatalf("progress %#v %v", p, e)
	}
}

func TestVerifyRejectsUnsafeAndDamagedBundles(t *testing.T) {
	for _, tc := range []string{"corrupt", "unexpected", "traversal", "absolute", "backslash", "duplicate", "case_duplicate", "reserved", "reserved_superscript", "parent_collision"} {
		t.Run(tc, func(t *testing.T) {
			d := bundle(t, "0.2.0", "ok")
			m, e := Verify(context.Background(), d)
			if e != nil {
				t.Fatal(e)
			}
			switch tc {
			case "corrupt":
				os.WriteFile(filepath.Join(d, "bin", "dotcontrol"), []byte("bad"), 0755)
			case "unexpected":
				os.WriteFile(filepath.Join(d, "surprise"), []byte("bad"), 0600)
			case "traversal":
				m.Files[0].Path = "../escape"
			case "absolute":
				m.Files[0].Path = "/escape"
			case "backslash":
				m.Files[0].Path = "bin\\dotcontrol"
			case "duplicate":
				m.Files = append(m.Files, m.Files[0])
			case "case_duplicate":
				f := m.Files[0]
				f.Path = "BIN/dotcontrol"
				m.Files = append(m.Files, f)
			case "reserved":
				m.Files[0].Path = "CON.txt"
			case "reserved_superscript":
				m.Files[0].Path = "COM¹.txt"
			case "parent_collision":
				f := m.Files[0]
				f.Path = "bin"
				m.Files = append(m.Files, f)
			}
			writeManifest(t, d, m)
			if _, e = Verify(context.Background(), d); e == nil {
				t.Fatal("unsafe bundle accepted")
			}
		})
	}
}

func TestReservedPortableDeviceNames(t *testing.T) {
	for _, p := range []string{"COM¹.txt", "LPT²", "bin/COM³.bin"} {
		if safeRelative(p) {
			t.Errorf("reserved Windows device name accepted: %s", p)
		}
	}
}

func TestRejectsLinks(t *testing.T) {
	t.Run("hardlink", func(t *testing.T) {
		d := bundle(t, "0.2.0", "ok")
		external := filepath.Join(t.TempDir(), "linked")
		if e := os.Link(filepath.Join(d, "bin", "dotcontrol"), external); e != nil {
			t.Skip(e)
		}
		if _, e := Verify(context.Background(), d); e == nil {
			t.Fatal("hardlink accepted")
		}
	})
	t.Run("symlink", func(t *testing.T) {
		d := bundle(t, "0.2.0", "ok")
		p := filepath.Join(d, "bin", "dotcontrol")
		os.Remove(p)
		if e := os.Symlink(filepath.Join(t.TempDir(), "outside"), p); e != nil {
			t.Skip(e)
		}
		if _, e := Verify(context.Background(), d); e == nil {
			t.Fatal("symlink accepted")
		}
	})
}

func TestResumeStageAndVersionConflict(t *testing.T) {
	o := opts(t, bundle(t, "0.2.0", "one"))
	ctx, cancel := context.WithCancel(context.Background())
	o.Progress = func(p Progress) {
		if p.Phase == "stage" {
			cancel()
		}
	}
	if _, e := Apply(ctx, o); !errors.Is(e, context.Canceled) {
		t.Fatalf("cancel: %v", e)
	}
	o.Progress = nil
	first, e := Apply(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	again, e := Apply(context.Background(), o)
	if e != nil || again.Current != first.Current {
		t.Fatalf("idempotent %#v %v", again, e)
	}
	o.BundleDir = bundle(t, "0.2.0", "different")
	if _, e = Apply(context.Background(), o); e == nil {
		t.Fatal("same version different bytes accepted")
	}
}

func TestRepairDamagedVersionAndPreserveStateOnRemove(t *testing.T) {
	o := opts(t, bundle(t, "0.2.0", "one"))
	if e := os.MkdirAll(o.StateDir, 0700); e != nil {
		t.Fatal(e)
	}
	state := filepath.Join(o.StateDir, "ledger.jsonl")
	os.WriteFile(state, []byte("keep"), 0600)
	r, e := Apply(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(r.Current.Dir, "bin", "dotcontrol"), []byte("broken"), 0755)
	repaired, e := Repair(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	data, e := os.ReadFile(filepath.Join(repaired.Current.Dir, "bin", "dotcontrol"))
	if e != nil || string(data) != "one" {
		t.Fatalf("repair %q %v", data, e)
	}
	extra := filepath.Join(repaired.Current.Dir, "user-note.txt")
	os.WriteFile(extra, []byte("preserve"), 0600)
	if e = Remove(context.Background(), RemoveOptions{Root: o.Root, StateDir: o.StateDir}); e != nil {
		t.Fatal(e)
	}
	for _, p := range []string{state, extra} {
		if _, e = os.Stat(p); e != nil {
			t.Fatalf("preserved %s: %v", p, e)
		}
	}
	if _, e = os.Stat(filepath.Join(repaired.Current.Dir, "bin", "dotcontrol")); !os.IsNotExist(e) {
		t.Fatal("owned file remains", e)
	}
	if _, e = ReadCurrent(o.Root); !os.IsNotExist(e) {
		t.Fatal("pointer remains", e)
	}
}

func TestRefuseStateWithinRoot(t *testing.T) {
	o := opts(t, bundle(t, "0.2.0", "one"))
	o.StateDir = filepath.Join(o.Root, "state")
	if _, e := Apply(context.Background(), o); e == nil {
		t.Fatal("state overlap accepted")
	}
	if e := Remove(context.Background(), RemoveOptions{Root: o.Root, StateDir: o.StateDir}); e == nil {
		t.Fatal("remove state overlap accepted")
	}
}

func TestInstallSerializesRoot(t *testing.T) {
	o := opts(t, bundle(t, "0.2.0", "one"))
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	o.Health = func(context.Context, Current) error { close(entered); <-release; return nil }
	go func() { _, e := Apply(context.Background(), o); done <- e }()
	<-entered
	second := o
	second.Health = nil
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, e := Apply(ctx, second)
	if !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("parallel install not blocked: %v", e)
	}
	close(release)
	if e = <-done; e != nil {
		t.Fatal(e)
	}
}

func TestInterruptedActivationRecoveredBeforeRetry(t *testing.T) {
	o := opts(t, bundle(t, "0.2.0", "old"))
	old, e := Apply(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	o.BundleDir = bundle(t, "0.2.1", "new")
	next, e := Apply(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	// Model termination after pointer activation, before health was acknowledged.
	p := Progress{Phase: "health", Previous: &old.Current, Target: &next.Current, RecoveryNeeded: true}
	if e = atomicJSON(filepath.Join(o.Root, "transaction.json"), p); e != nil {
		t.Fatal(e)
	}
	o.BundleDir = bundle(t, "0.2.2", "retry")
	o.BeforeActivate = func(_ context.Context, _ Current) error {
		c, e := ReadCurrent(o.Root)
		if e != nil || c != old.Current {
			t.Fatalf("interrupted release remained active: %#v %v", c, e)
		}
		return nil
	}
	if _, e = Apply(context.Background(), o); e != nil {
		t.Fatal(e)
	}
}

func TestUninstallCanRepeatAndReinstallWithUnownedFiles(t *testing.T) {
	o := opts(t, bundle(t, "0.2.0", "one"))
	r, e := Apply(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	extra := filepath.Join(r.Current.Dir, "notes.txt")
	os.WriteFile(extra, []byte("keep"), 0600)
	remove := RemoveOptions{Root: o.Root, StateDir: o.StateDir}
	if e = Remove(context.Background(), remove); e != nil {
		t.Fatal(e)
	}
	if e = Remove(context.Background(), remove); e != nil {
		t.Fatalf("second uninstall: %v", e)
	}
	if _, e = Repair(context.Background(), o); e != nil {
		t.Fatalf("reinstall: %v", e)
	}
	if b, e := os.ReadFile(extra); e != nil || string(b) != "keep" {
		t.Fatalf("unowned data changed: %q %v", b, e)
	}
}

func TestInterruptedStageResumesAndRejectsUnownedStagingPaths(t *testing.T) {
	for _, unsafe := range []bool{false, true} {
		t.Run(fmt.Sprint(unsafe), func(t *testing.T) {
			o := opts(t, bundle(t, "0.2.0", "one"))
			m, e := Verify(context.Background(), o.BundleDir)
			if e != nil {
				t.Fatal(e)
			}
			stage := filepath.Join(o.Root, ".staging", manifestHash(m))
			os.MkdirAll(filepath.Join(stage, "bin"), 0700)
			os.WriteFile(filepath.Join(stage, "bin", "dotcontrol"), []byte("half"), 0755)
			if unsafe {
				os.WriteFile(filepath.Join(stage, "unexpected.txt"), []byte("keep"), 0600)
			}
			_, e = Apply(context.Background(), o)
			if unsafe && e == nil {
				t.Fatal("unexpected staging object accepted")
			}
			if !unsafe && e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestMacStyleStateParentAndRollbackCallback(t *testing.T) {
	o := opts(t, bundle(t, "0.2.0", "one"))
	o.StateDir = filepath.Dir(o.Root)
	var rolledBack bool
	o.Health = func(context.Context, Current) error { return errors.New("health failure") }
	o.Rollback = func(_ context.Context, c *Current) error {
		rolledBack = true
		if c != nil {
			t.Fatal("unexpected previous version")
		}
		if _, e := ReadCurrent(o.Root); !os.IsNotExist(e) {
			t.Fatalf("pointer wasn't removed: %v", e)
		}
		return nil
	}
	if _, e := Apply(context.Background(), o); e == nil || !rolledBack {
		t.Fatalf("rollback callback missing: %v", e)
	}
}

func TestRollbackRestoresServiceOnBeforeActivateFailure(t *testing.T) {
	o := opts(t, bundle(t, "0.2.0", "one"))
	old, e := Apply(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	o.BundleDir = bundle(t, "0.2.1", "two")
	var restored bool
	o.BeforeActivate = func(context.Context, Current) error { return errors.New("partially stopped service") }
	o.Rollback = func(_ context.Context, c *Current) error {
		restored = true
		if c == nil || *c != old.Current {
			t.Fatal("wrong rollback target")
		}
		return nil
	}
	if _, e = Apply(context.Background(), o); e == nil {
		t.Fatal("accepted before-activation failure")
	}
	if !restored {
		t.Fatal("previous runtime was not restored")
	}
}

func TestRecoveryRestartsServiceAndRetainsFailedRollback(t *testing.T) {
	o := opts(t, bundle(t, "0.2.0", "one"))
	old, e := Apply(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	o.BundleDir = bundle(t, "0.2.1", "two")
	next, e := Apply(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	p := Progress{Phase: "health", Previous: &old.Current, Target: &next.Current, RecoveryNeeded: true}
	if e = atomicJSON(filepath.Join(o.Root, "transaction.json"), p); e != nil {
		t.Fatal(e)
	}
	var attempts int
	o.Rollback = func(_ context.Context, c *Current) error {
		attempts++
		if c == nil || *c != old.Current {
			t.Fatal("wrong previous runtime")
		}
		return errors.New("restart failed")
	}
	if _, e = Apply(context.Background(), o); e == nil {
		t.Fatal("recovery callback failure was ignored")
	}
	if attempts != 1 {
		t.Fatal("recovery callback was not invoked")
	}
	p, e = ReadProgress(o.Root)
	if e != nil || !p.RecoveryNeeded {
		t.Fatalf("failed rollback was forgotten: %#v %v", p, e)
	}
	o.Rollback = func(context.Context, *Current) error { attempts++; return nil }
	if _, e = Apply(context.Background(), o); e != nil {
		t.Fatal(e)
	}
	if attempts != 2 {
		t.Fatal("failed recovery was not retried")
	}
}
