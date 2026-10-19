package dotevents

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"
)

func validateStorePath(dir string) error {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || len(dir) > 32768 || strings.ContainsFunc(dir, unicode.IsControl) {
		return errf("unknown")
	}
	vol := filepath.VolumeName(dir)
	if dir == vol+string(os.PathSeparator) {
		return errf("unknown")
	}
	if runtime.GOOS == "windows" {
		if len(vol) != 2 || vol[1] != ':' || strings.Contains(dir[2:], ":") || strings.Contains(dir, "/") {
			return errf("unknown")
		}
		for _, part := range strings.Split(dir[3:], `\`) {
			if part == "" || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
				return errf("unknown")
			}
			name := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
			if name == "CON" || name == "PRN" || name == "AUX" || name == "NUL" || (len(name) == 4 && (strings.HasPrefix(name, "COM") || strings.HasPrefix(name, "LPT")) && name[3] >= '1' && name[3] <= '9') {
				return errf("unknown")
			}
		}
	}
	return nil
}
func checkAncestors(dir string, create bool) error {
	if e := validateStorePath(dir); e != nil {
		return e
	}
	var chain []string
	for p := dir; ; p = filepath.Dir(p) {
		chain = append(chain, p)
		if filepath.Dir(p) == p {
			break
		}
	}
	for i := len(chain) - 1; i >= 0; i-- {
		p := chain[i]
		info, e := os.Lstat(p)
		if os.IsNotExist(e) && create {
			if e = os.Mkdir(p, 0700); e != nil {
				return errf("unknown")
			}
			info, e = os.Lstat(p)
		}
		if e != nil || !info.IsDir() || rejectLink(info) != nil {
			return errf("unknown")
		}
	}
	return privatePath(dir, true)
}

// Recheck and protect owned files on reads as well as writes. The enclosing
// directory is private; this follows the repository's same-user threat model.
func protectFile(path string, required bool) error {
	info, e := os.Lstat(path)
	if os.IsNotExist(e) && !required {
		return nil
	}
	if e != nil || !info.Mode().IsRegular() || rejectLink(info) != nil {
		return errf("corrupt")
	}
	f, e := os.Open(path)
	if e != nil {
		return errf("corrupt")
	}
	fi, e := f.Stat()
	if e != nil || rejectHardlink(f, fi) != nil {
		f.Close()
		return errf("corrupt")
	}
	if e = f.Close(); e != nil {
		return errf("corrupt")
	}
	if privatePath(path, false) != nil {
		return errf("corrupt")
	}
	return nil
}
