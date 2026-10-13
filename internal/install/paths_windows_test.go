//go:build windows

package install

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestWindowsNamespaceAliasCannotHideStateInsideRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "program")
	if e := os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	stateFile := filepath.Join(root, "ledger.jsonl")
	if e := os.WriteFile(stateFile, []byte("preserve"), 0600); e != nil {
		t.Fatal(e)
	}
	plain, e := os.Stat(root)
	if e != nil {
		t.Fatal(e)
	}
	extended := `\\?\` + root
	alias, e := os.Stat(extended)
	if e != nil {
		t.Fatal(e)
	}
	if !os.SameFile(plain, alias) {
		t.Fatal("fixture is not the same filesystem directory")
	}
	for _, tc := range []struct{ name, root, state string }{
		{"extended_root_equal", extended, root},
		{"extended_state_equal", root, extended},
		{"extended_root_contains", extended, filepath.Join(root, "state")},
		{"extended_state_inside", root, extended + `\state`},
		{"device_root", `\\.\` + root, root},
		{"native_root", `\??\` + root, root},
		{"slash_namespace_root", strings.ReplaceAll(extended, `\`, "/"), root},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, e := Apply(context.Background(), Options{Root: tc.root, StateDir: tc.state, BundleDir: bundle(t, "0.2.0", "one")}); e == nil {
				t.Fatal("namespace path accepted by install")
			}
			if e := Remove(context.Background(), RemoveOptions{Root: tc.root, StateDir: tc.state}); e == nil {
				t.Fatal("namespace path accepted by uninstall")
			}
			if got, e := os.ReadFile(stateFile); e != nil || string(got) != "preserve" {
				t.Fatalf("state changed: %q %v", got, e)
			}
		})
	}
}

func TestCanonicalPathRetainsWindowsNamespaceRejection(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{`\\?\` + root, `\\.\` + root, `\??\` + root, strings.ReplaceAll(`\\?\`+root, `\`, "/")} {
		if _, e := CanonicalPath(p); e == nil {
			t.Fatalf("namespace accepted: %q", p)
		}
	}
}

func shortDirectory(t *testing.T, p string) string {
	t.Helper()
	ptr, e := syscall.UTF16PtrFromString(p)
	if e != nil {
		t.Fatal(e)
	}
	buf := make([]uint16, 32768)
	n, e := syscall.GetShortPathName(ptr, &buf[0], uint32(len(buf)))
	if e != nil {
		t.Fatal(e)
	}
	if n == 0 || n >= uint32(len(buf)) {
		t.Fatal("invalid short path result")
	}
	short := syscall.UTF16ToString(buf[:n])
	if strings.EqualFold(short, p) {
		t.Skip("volume has no distinct 8.3 alias for fixture")
	}
	longInfo, e := os.Stat(p)
	if e != nil {
		t.Fatal(e)
	}
	shortInfo, e := os.Stat(short)
	if e != nil {
		t.Fatal(e)
	}
	if !os.SameFile(longInfo, shortInfo) {
		t.Fatal("short fixture is not physically identical")
	}
	return short
}

func TestWindowsShortNameCannotHideStateInsideRoot(t *testing.T) {
	long := filepath.Join(t.TempDir(), "long parent directory", "program-long-name")
	if e := os.MkdirAll(long, 0700); e != nil {
		t.Fatal(e)
	}
	short := shortDirectory(t, long)
	for _, tc := range []struct{ name, root, state string }{
		{"equal", short, long},
		{"reverse_equal", long, short},
		{"missing_state_child", short, filepath.Join(long, "state")},
		{"reverse_missing_state_child", long, filepath.Join(short, "state")},
		{"both_missing_equal", filepath.Join(short, "new program"), filepath.Join(long, "new program")},
		{"both_missing_nested", filepath.Join(short, "new program"), filepath.Join(long, "new program", "state")},
		{"reverse_both_missing_nested", filepath.Join(long, "new program"), filepath.Join(short, "new program", "state")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, e := preparePaths(tc.root, tc.state); e == nil {
				t.Fatal("8.3 alias bypassed state containment")
			}
		})
	}
}

func TestWindowsShortNameInstallUsesCanonicalPointer(t *testing.T) {
	base := filepath.Join(t.TempDir(), "long parent directory")
	if e := os.MkdirAll(base, 0700); e != nil {
		t.Fatal(e)
	}
	short := shortDirectory(t, base)
	o := Options{Root: filepath.Join(short, "program"), StateDir: filepath.Join(base, "state"), BundleDir: bundle(t, "0.2.0", "one")}
	r, e := Apply(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	for _, root := range []string{o.Root, filepath.Join(base, "program")} {
		c, e := ReadCurrent(root)
		if e != nil || c != r.Current {
			t.Fatalf("same install cannot be read through %s: %#v %v", root, c, e)
		}
	}
}

func TestWindowsNamespacePathsRejectedBeforeFilesystemAccess(t *testing.T) {
	for _, p := range []string{`\\?\UNC\server\share\program`, `\\.\UNC\server\share\program`, `\\?\GLOBALROOT\Device\HarddiskVolume1\program`} {
		if _, e := preparePaths(p, `C:\state`); e == nil || !strings.Contains(e.Error(), "namespace") {
			t.Errorf("namespace not rejected before filesystem access: %s: %v", p, e)
		}
	}
}

func TestWindowsCleanupRejectsJunctionAncestor(t *testing.T) {
	o := opts(t, bundle(t, "0.2.0", "old"))
	old, e := Apply(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	outside := t.TempDir()
	file := filepath.Join(outside, "dotcontrol")
	if e = os.WriteFile(file, []byte("old"), 0755); e != nil {
		t.Fatal(e)
	}
	bin := filepath.Join(old.Current.Dir, "bin")
	if e = os.Rename(bin, filepath.Join(old.Current.Dir, "saved-bin")); e != nil {
		t.Fatal(e)
	}
	output, e := exec.Command("cmd", "/c", "mklink", "/J", bin, outside).CombinedOutput()
	if e != nil {
		t.Fatalf("create junction fixture: %s %v", output, e)
	}
	defer os.Remove(bin)
	o.BundleDir = bundle(t, "0.2.1", "new")
	enableCleanup(t, &o, func(context.Context, Current) error { return nil })
	r, e := Apply(context.Background(), o)
	assertCleanupPending(t, o, r, e)
	if b, e := os.ReadFile(file); e != nil || string(b) != "old" {
		t.Fatalf("junction target changed: %q %v", b, e)
	}
}

func TestWindowsCleanupLockedFileKeepsNewCurrentAndRetries(t *testing.T) {
	o := opts(t, bundle(t, "0.2.0", "old"))
	old, e := Apply(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	ptr, e := syscall.UTF16PtrFromString(filepath.Join(old.Current.Dir, "bin", "dotcontrol"))
	if e != nil {
		t.Fatal(e)
	}
	h, e := syscall.CreateFile(ptr, syscall.GENERIC_READ, syscall.FILE_SHARE_READ, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = syscall.CloseHandle(h) })
	o.BundleDir = bundle(t, "0.2.1", "new")
	enableCleanup(t, &o, func(context.Context, Current) error { return nil })
	r, e := Apply(context.Background(), o)
	assertCleanupPending(t, o, r, e)
	if e = syscall.CloseHandle(h); e != nil {
		t.Fatal(e)
	}
	h = syscall.InvalidHandle
	o.BundleDir = ""
	next, e := Repair(context.Background(), o)
	if e != nil || next.Current != r.Current {
		t.Fatalf("locked-file retry: %#v %v", next, e)
	}
	if _, e = os.Stat(old.Current.Dir); !os.IsNotExist(e) {
		t.Fatalf("old generation remains: %v", e)
	}
}
