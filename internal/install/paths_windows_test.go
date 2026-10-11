//go:build windows

package install

import (
	"context"
	"os"
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
