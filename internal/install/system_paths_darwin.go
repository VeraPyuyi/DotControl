//go:build darwin

package install

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Darwin exposes its standard temporary directories through these root-owned
// OS aliases. Normalize only these exact, verified mappings; never resolve an
// arbitrary symlink in the caller's install, state or bundle path.
func normalizeSystemPath(p string) (string, error) {
	for _, mapping := range [][2]string{{"/var", "/private/var"}, {"/tmp", "/private/tmp"}} {
		alias, destination := mapping[0], mapping[1]
		if p != alias && !strings.HasPrefix(p, alias+"/") {
			continue
		}
		info, e := os.Lstat(alias)
		if e != nil {
			return "", e
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return p, nil
		}
		target, e := os.Readlink(alias)
		if e != nil {
			return "", e
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(alias), target)
		}
		if filepath.Clean(target) != destination {
			return "", errors.New("unexpected Darwin system alias target")
		}
		physical, e := os.Lstat(destination)
		if e != nil {
			return "", e
		}
		if !physical.IsDir() || physical.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("Darwin system alias destination is not a real directory")
		}
		resolved, e := os.Stat(alias)
		if e != nil {
			return "", e
		}
		if !os.SameFile(resolved, physical) {
			return "", errors.New("Darwin system alias changed during validation")
		}
		return destination + p[len(alias):], nil
	}
	return p, nil
}
