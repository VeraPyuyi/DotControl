package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func cleanupCurrentMatches(root string, target Current) error {
	current, e := ReadCurrent(root)
	if e != nil {
		return e
	}
	if current != target {
		return errors.New("current pointer differs from committed cleanup target")
	}
	return nil
}

func cleanupOldVersions(ctx context.Context, root string, own *ownership, target Current, guard func(context.Context, Current) error) error {
	for i := 0; i < len(own.Versions); {
		v := own.Versions[i]
		dir := filepath.Join(root, "versions", v.Dir)
		if strings.EqualFold(filepath.Clean(dir), filepath.Clean(target.Dir)) {
			i++
			continue
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		if e := cleanupCurrentMatches(root, target); e != nil {
			return e
		}
		if e := checkAncestors(dir); e != nil {
			return e
		}
		candidate := Current{Version: v.Manifest.Version, OS: v.Manifest.OS, Arch: v.Manifest.Arch, ManifestSHA256: v.Hash, Dir: dir}
		if guard == nil {
			return errors.New("old-version cleanup requires a BeforeCleanup reference guard")
		}
		if e := guard(ctx, candidate); e != nil {
			return fmt.Errorf("cleanup guard for %s: %w", v.Dir, e)
		}
		if e := cleanupCurrentMatches(root, target); e != nil {
			return e
		}
		if e := removeOwnedGeneration(ctx, dir, v); e != nil {
			return fmt.Errorf("cleanup %s: %w", v.Dir, e)
		}
		// Persist retirement only after every recorded payload is absent. Missing
		// files are accepted on retry after a crash between deletion and this write.
		remaining := append([]ownedVersion(nil), own.Versions[:i]...)
		remaining = append(remaining, own.Versions[i+1:]...)
		updated := *own
		updated.Versions = remaining
		if e := atomicJSON(filepath.Join(root, ownerName), updated); e != nil {
			return e
		}
		*own = updated
	}
	return nil
}

// This is intentionally separate from uninstall: upgrade cleanup never blindly
// removes a listed path whose bytes/type have changed since installation.
func removeOwnedGeneration(ctx context.Context, dir string, v ownedVersion) error {
	if e := checkAncestors(dir); e != nil {
		return e
	}
	parents := map[string]bool{dir: true}
	for _, spec := range v.Manifest.Files {
		if e := ctx.Err(); e != nil {
			return e
		}
		p := filepath.Join(dir, filepath.FromSlash(spec.Path))
		if e := checkAncestors(p); e != nil {
			return e
		}
		for parent := filepath.Dir(p); within(dir, parent); parent = filepath.Dir(parent) {
			parents[parent] = true
			if parent == dir {
				break
			}
		}
		if _, e := os.Lstat(p); os.IsNotExist(e) {
			continue
		} else if e != nil {
			return e
		}
		if e := verifyFile(ctx, p, spec); e != nil {
			return fmt.Errorf("owned file changed or unsafe; preserved %s: %w", spec.Path, e)
		}
		if e := checkAncestors(p); e != nil {
			return e
		}
		if e := os.Remove(p); e != nil {
			return e
		}
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	p := filepath.Join(dir, "manifest.json")
	if e := checkAncestors(p); e != nil {
		return e
	}
	if _, e := os.Lstat(p); e == nil {
		m, e := readManifest(dir)
		if e != nil {
			return e
		}
		if manifestHash(m) != v.Hash {
			return errors.New("owned manifest changed; preserved")
		}
		if e = os.Remove(p); e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	// Remove only empty parents named by owned paths. Unknown children keep
	// their directories; they are neither traversed nor claimed by the installer.
	dirs := make([]string, 0, len(parents))
	for parent := range parents {
		dirs = append(dirs, parent)
	}
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	for _, parent := range dirs {
		if e := checkAncestors(parent); e != nil {
			return e
		}
		entries, e := os.ReadDir(parent)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return e
		}
		if len(entries) == 0 {
			if e = os.Remove(parent); e != nil && !os.IsNotExist(e) {
				return e
			}
		}
	}
	return nil
}
