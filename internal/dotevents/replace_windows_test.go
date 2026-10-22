//go:build windows

package dotevents

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// Windows ACL propagation and scanners can hold a file without delete sharing.
// A transient reader must not cause a locked store update to fail.
func TestWindowsReplaceAfterTransientReader(t *testing.T) {
	dir := t.TempDir()
	target, source := filepath.Join(dir, "state"), filepath.Join(dir, "next")
	if err := os.WriteFile(target, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(target)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() { time.Sleep(50 * time.Millisecond); windows.CloseHandle(handle); close(released) }()
	defer func() { <-released }()
	err = replaceFile(source, target)
	if err != nil {
		t.Fatal("transient reader prevented replacement:", err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "new" {
		t.Fatalf("replacement contents = %q, error = %v", data, err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("source still present: %v", err)
	}
}

func TestWindowsReplaceMissingSource(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "state")
	if err := os.WriteFile(target, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := replaceFile(filepath.Join(dir, "missing"), target); err != windows.ERROR_FILE_NOT_FOUND {
		t.Fatalf("missing source: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "old" {
		t.Fatalf("original contents = %q, error = %v", data, err)
	}
}
