//go:build windows

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/install"
	"github.com/dotcontrol/dotcontrol/internal/model"
)

func shortInstallerPath(t *testing.T, p string) string {
	t.Helper()
	ptr, err := syscall.UTF16PtrFromString(p)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, 32768)
	n, err := syscall.GetShortPathName(ptr, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || n >= uint32(len(buf)) {
		t.Fatalf("short path fixture: length=%d err=%v", n, err)
	}
	short := syscall.UTF16ToString(buf[:n])
	if strings.EqualFold(short, p) {
		t.Skip("volume has no distinct 8.3 alias for fixture")
	}
	a, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.Stat(short)
	if err != nil || !os.SameFile(a, b) {
		t.Fatalf("short path fixture is not the same filesystem object: %v", err)
	}
	return short
}

func TestWindowsExecutableDigestAcceptsShortNameWithoutLinks(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, agentName())
	if err := os.WriteFile(p, []byte("unchanged executable"), 0755); err != nil {
		t.Fatal(err)
	}
	short := filepath.Join(shortInstallerPath(t, dir), agentName())
	resolved, err := filepath.EvalSymlinks(short)
	if err != nil || sameProgramPath(resolved, short) {
		t.Fatalf("fixture did not exercise short-name expansion: short=%q resolved=%q err=%v", short, resolved, err)
	}
	for _, path := range []string{p, short} {
		got, err := regularExecutableDigest(path)
		if err != nil || got != fileDigestForTest(t, p) {
			t.Fatalf("unchanged executable rejected: path=%q digest=%q err=%v", path, got, err)
		}
	}
}

func TestWindowsLegacyRollbackWithShortTempPath(t *testing.T) {
	short := shortInstallerPath(t, t.TempDir())
	t.Setenv("TMP", short)
	t.Setenv("TEMP", short)
	t.Run("failed_health", TestUnmanagedActivationFailureRestoresExactConfigStartupAndLegacy)
	t.Run("interrupted_activation", TestInterruptedUnmanagedActivationRecoversFromDurableSnapshot)
}

func TestWindowsLegacyReferenceUsesCanonicalExecutable(t *testing.T) {
	dir := t.TempDir()
	short := filepath.Join(shortInstallerPath(t, dir), agentName())
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go.exe")
	if out, err := exec.CommandContext(ctx, goBinary, "build", "-o", short, ".").CombinedOutput(); err != nil {
		t.Fatalf("legacy fixture build: %v %s", err, out)
	}
	want, err := install.CanonicalPath(short)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := verifyLegacyExecutable(ctx, short)
	if err != nil || ref.Executable != want || ref.SHA256 != fileDigestForTest(t, want) || ref.Version != model.Version {
		t.Fatalf("legacy reference cannot identify canonical runtime: got=%+v want=%q err=%v", ref, want, err)
	}
}

func TestWindowsExecutableDigestRejectsOriginalJunctionAndNamespace(t *testing.T) {
	base, outside := t.TempDir(), t.TempDir()
	p := filepath.Join(outside, agentName())
	if err := os.WriteFile(p, []byte("unchanged executable"), 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "legacy")
	output, err := exec.Command("cmd", "/c", "mklink", "/J", link, outside).CombinedOutput()
	if err != nil {
		t.Fatalf("create junction fixture: %s %v", output, err)
	}
	t.Cleanup(func() { _ = os.Remove(link) })
	for _, path := range []string{filepath.Join(link, agentName()), `\\?\` + p, `\\.\` + p} {
		if _, err := regularExecutableDigest(path); err == nil {
			t.Fatalf("unsafe executable path accepted: %q", path)
		}
	}
	if got := fileDigestForTest(t, p); got == "" {
		t.Fatal("target unexpectedly missing")
	}
}
