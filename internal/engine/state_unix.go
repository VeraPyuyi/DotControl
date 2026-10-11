//go:build !windows

package engine

import "os"

func privateDirectory(path string) error { return os.Chmod(path, 0700) }
func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fail("storage_unavailable")
	}
	defer f.Close()
	if err = f.Sync(); err != nil {
		return fail("storage_unavailable")
	}
	return nil
}
