//go:build !windows

package dotbridge

import (
	"os"
	"syscall"
)

func pairingPrivatePath(path string, directory bool) error {
	mode := os.FileMode(0600)
	if directory {
		mode = 0700
	}
	return os.Chmod(path, mode)
}
func pairingRejectLink(info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return errPrivateBridgeFile
	}
	return nil
}
func pairingRejectHardlink(_ *os.File, info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Nlink != 1 {
		return errPrivateBridgeFile
	}
	return nil
}
func pairingReplace(from, to string) error { return os.Rename(from, to) }
