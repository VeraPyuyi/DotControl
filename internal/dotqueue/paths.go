package dotqueue

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// Paths must be canonical local absolute non-root directories. Ancestors must
// be real directories; the caller supplies trusted parents owned/administered
// by the local user or OS. This is not isolation from a malicious same-user process.
func localDir(dir string) bool {
	if !filepath.IsAbs(dir) || len(dir) > 32768 || filepath.Clean(dir) != dir || strings.ContainsFunc(dir, unicode.IsControl) {
		return false
	}
	vol := filepath.VolumeName(dir)
	if strings.HasPrefix(vol, `\\`) || dir == filepath.VolumeName(dir)+string(filepath.Separator) {
		return false
	}
	rest := strings.TrimPrefix(dir, vol)
	if strings.Contains(rest, ":") {
		return false
	}
	return true
}
func checkAncestors(dir string) error {
	for p := dir; ; p = filepath.Dir(p) {
		i, e := os.Lstat(p)
		if e == nil {
			if !i.IsDir() || rejectLink(i) != nil {
				return errf("unknown")
			}
		} else if !os.IsNotExist(e) {
			return errf("unknown")
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return nil
}
func (s *Store) readPrivate(path string, limit int64) ([]byte, error) {
	if regularFile(path) != nil {
		return nil, errf("corrupt")
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, errf("corrupt")
	}
	defer f.Close()
	i, e := f.Stat()
	if e != nil || !i.Mode().IsRegular() || rejectHardlink(f, i) != nil || i.Size() > limit {
		return nil, errf("corrupt")
	}
	if privatePath(path, false) != nil {
		return nil, errf("unknown")
	}
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil || int64(len(b)) > limit {
		return nil, errf("corrupt")
	}
	return b, nil
}

type ConfigStatus struct {
	Enabled bool `json:"enabled"`
}

func (s *Store) ConfigStatus() (ConfigStatus, error) {
	var out ConfigStatus
	e := s.withLock(false, false, func(st *persistenceState) error { out.Enabled = st.Enabled; return nil })
	return out, e
}
