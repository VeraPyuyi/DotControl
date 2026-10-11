//go:build !windows

package install

import (
	"errors"
	"os"
	"syscall"
)

func rejectPathNamespace(string) error               { return nil }
func canonicalExistingPath(p string) (string, error) { return p, nil }

func rejectLink(info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("symlink is forbidden")
	}
	return nil
}
func rejectHardlink(_ *os.File, info os.FileInfo) error {
	s, ok := info.Sys().(*syscall.Stat_t)
	if !ok || s.Nlink != 1 {
		return errors.New("hardlink is forbidden")
	}
	return nil
}
