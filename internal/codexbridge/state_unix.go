//go:build !windows

package codexbridge

import "os"

func privateDirectory(path string) error { return os.Chmod(path, 0700) }
func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
