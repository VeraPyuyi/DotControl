//go:build !windows

package dotevents

import (
	"os"
	"syscall"
)

func privatePath(path string, directory bool) error {
	mode := os.FileMode(0600)
	if directory {
		mode = 0700
	}
	return os.Chmod(path, mode)
}

func rejectLink(info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return errf("unknown")
	}
	return nil
}

func rejectHardlink(f *os.File, info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Nlink != 1 {
		return errf("unknown")
	}
	return nil
}

func replaceFile(from, to string) error { return os.Rename(from, to) }

func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
