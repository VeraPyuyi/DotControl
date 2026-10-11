//go:build windows

package install

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Explicit DOS/NT namespace spellings change Windows path parsing and can name
// the same directory as an ordinary drive/UNC path while escaping lexical
// containment checks. Callers use ordinary paths; Go handles long-path I/O.
func rejectPathNamespace(p string) error {
	p = strings.ReplaceAll(p, "/", `\`)
	for _, prefix := range []string{`\\?\`, `\\.\`, `\??\`, `\\??\`} {
		if strings.HasPrefix(p, prefix) {
			return errors.New("explicit Windows namespace paths are not supported for install or state directories")
		}
	}
	return nil
}

// Resolve the nearest existing ancestor by handle, then append nonexistent
// children. This collapses 8.3 names (and volume aliases) before containment is
// compared, without requiring the install/state directories to exist yet.
// canonicalInstallPath has already rejected every original reparse ancestor.
func canonicalExistingPath(p string) (string, error) {
	ancestor := p
	var suffix []string
	for {
		info, e := os.Lstat(ancestor)
		if e == nil {
			if e = rejectLink(info); e != nil {
				return "", e
			}
			f, e := os.Open(ancestor)
			if e != nil {
				return "", e
			}
			defer f.Close()
			opened, e := f.Stat()
			if e != nil {
				return "", e
			}
			if !os.SameFile(info, opened) {
				return "", errors.New("path changed during canonicalization")
			}
			buf := make([]uint16, 32768)
			n, e := windows.GetFinalPathNameByHandle(windows.Handle(f.Fd()), &buf[0], uint32(len(buf)), 0)
			if e != nil {
				return "", e
			}
			if n == 0 || n >= uint32(len(buf)) {
				return "", errors.New("invalid canonical path length")
			}
			resolved := windows.UTF16ToString(buf[:n])
			if strings.HasPrefix(strings.ToUpper(resolved), `\\?\UNC\`) {
				resolved = `\\` + resolved[8:]
			} else if strings.HasPrefix(resolved, `\\?\`) {
				resolved = resolved[4:]
			}
			if !filepath.IsAbs(resolved) {
				return "", errors.New("canonical path is not absolute")
			}
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(e) {
			return "", e
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return "", e
		}
		suffix = append(suffix, filepath.Base(ancestor))
		ancestor = parent
	}
}

func rejectLink(info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("symlink is forbidden")
	}
	if d, ok := info.Sys().(*syscall.Win32FileAttributeData); ok && d.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("reparse point is forbidden")
	}
	return nil
}
func rejectHardlink(f *os.File, _ os.FileInfo) error {
	var info windows.ByHandleFileInformation
	if e := windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); e != nil {
		return e
	}
	if info.NumberOfLinks != 1 {
		return errors.New("hardlink is forbidden")
	}
	return nil
}
