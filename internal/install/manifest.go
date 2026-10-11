// Package install verifies and transactionally activates prebuilt DotControl
// bundles. It never migrates application state or accesses the credential vault.
package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Mode   uint32 `json:"mode"`
}
type Manifest struct {
	Version string `json:"version"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	Files   []File `json:"files"`
}

func safeRelative(p string) bool {
	if p == "" || len(p) > 1024 || path.IsAbs(p) || path.Clean(p) != p || strings.ContainsAny(p, "\\:\x00") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == "." || part == ".." || part == "" || strings.TrimRight(part, ". ") != part {
			return false
		}
		for _, c := range part {
			if c < 32 || strings.ContainsRune(`<>"|?*`, c) {
				return false
			}
		}
		stem := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || stem == "CLOCK$" {
			return false
		}
		device := []rune(stem)
		if len(device) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && strings.ContainsRune("123456789¹²³", device[3]) {
			return false
		}
	}
	return true
}
func validateManifest(m Manifest) error {
	if len(m.Version) == 0 || len(m.Version) > 80 || !safeRelative(m.Version) || strings.Contains(m.Version, "/") {
		return errors.New("invalid version")
	}
	for _, c := range m.Version {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_') {
			return errors.New("invalid version")
		}
	}
	if m.OS != runtime.GOOS || m.Arch != runtime.GOARCH {
		return fmt.Errorf("bundle target %s/%s does not match %s/%s", m.OS, m.Arch, runtime.GOOS, runtime.GOARCH)
	}
	if len(m.Files) == 0 || len(m.Files) > 100000 {
		return errors.New("invalid file count")
	}
	paths := map[string]bool{}
	for _, f := range m.Files {
		key := strings.ToLower(f.Path)
		if !safeRelative(f.Path) || key == "manifest.json" || paths[key] {
			return fmt.Errorf("invalid or duplicate manifest path: %q", f.Path)
		}
		h, e := hex.DecodeString(f.SHA256)
		if e != nil || len(h) != sha256.Size || strings.ToLower(f.SHA256) != f.SHA256 {
			return fmt.Errorf("invalid sha256 for %q", f.Path)
		}
		if f.Mode == 0 || f.Mode&^0777 != 0 {
			return fmt.Errorf("invalid mode for %q", f.Path)
		}
		paths[key] = true
	}
	for p := range paths {
		for parent := path.Dir(p); parent != "."; parent = path.Dir(parent) {
			if paths[parent] {
				return fmt.Errorf("file/directory conflict: %q", p)
			}
		}
	}
	return nil
}
func readManifest(dir string) (Manifest, error) {
	var m Manifest
	f, e := openRegular(filepath.Join(dir, "manifest.json"))
	if e != nil {
		return m, e
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, 16<<20))
	dec.DisallowUnknownFields()
	if e = dec.Decode(&m); e != nil {
		return m, fmt.Errorf("manifest: %w", e)
	}
	var tail any
	if e = dec.Decode(&tail); e != io.EOF {
		return m, errors.New("manifest contains trailing data")
	}
	if e = validateManifest(m); e != nil {
		return m, e
	}
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Path < m.Files[j].Path })
	return m, nil
}
func manifestHash(m Manifest) string {
	b, _ := json.Marshal(m)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Verify checks the platform, exact file inventory, hashes, modes and absence of
// symlinks, reparse points and hardlinks. No files are modified.
func Verify(ctx context.Context, dir string) (Manifest, error) {
	var empty Manifest
	if e := checkAncestors(dir); e != nil {
		return empty, e
	}
	m, e := readManifest(dir)
	if e != nil {
		return m, e
	}
	if e = verifyTree(ctx, dir, m, true); e != nil {
		return m, e
	}
	return m, nil
}
func verifyTree(ctx context.Context, dir string, m Manifest, requireAll bool) error {
	files := map[string]File{}
	dirs := map[string]bool{".": true}
	for _, f := range m.Files {
		files[f.Path] = f
		for p := path.Dir(f.Path); p != "."; p = path.Dir(p) {
			dirs[p] = true
		}
	}
	found := map[string]bool{}
	e := filepath.WalkDir(dir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		info, e := os.Lstat(p)
		if e != nil {
			return e
		}
		if e = rejectLink(info); e != nil {
			return fmt.Errorf("%s: %w", p, e)
		}
		rel, e := filepath.Rel(dir, p)
		if e != nil {
			return e
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if !dirs[rel] {
				return fmt.Errorf("unexpected directory: %s", rel)
			}
			return nil
		}
		if rel == "manifest.json" {
			_, e := openAndCloseRegular(p)
			return e
		}
		spec, ok := files[rel]
		if !ok {
			return fmt.Errorf("unexpected file: %s", rel)
		}
		if e = verifyFile(ctx, p, spec); e != nil {
			return e
		}
		found[rel] = true
		return nil
	})
	if e != nil {
		return e
	}
	if requireAll && len(found) != len(files) {
		return errors.New("bundle is missing a manifest file")
	}
	return nil
}
func verifyFile(ctx context.Context, p string, spec File) error {
	f, e := openRegular(p)
	if e != nil {
		return e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return e
	}
	if runtime.GOOS != "windows" && uint32(info.Mode().Perm()) != spec.Mode {
		return fmt.Errorf("file mode mismatch: %s", spec.Path)
	}
	h := sha256.New()
	if _, e = io.Copy(h, &contextReader{ctx: ctx, r: f}); e != nil {
		return e
	}
	if hex.EncodeToString(h.Sum(nil)) != spec.SHA256 {
		return fmt.Errorf("sha256 mismatch: %s", spec.Path)
	}
	return nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.r.Read(p)
}
func openAndCloseRegular(p string) (os.FileInfo, error) {
	f, e := openRegular(p)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	return f.Stat()
}
func openRegular(p string) (*os.File, error) {
	if e := checkAncestors(p); e != nil {
		return nil, e
	}
	before, e := os.Lstat(p)
	if e != nil {
		return nil, e
	}
	if !before.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", p)
	}
	f, e := os.Open(p)
	if e != nil {
		return nil, e
	}
	after, e := f.Stat()
	if e != nil {
		f.Close()
		return nil, e
	}
	if !os.SameFile(before, after) {
		f.Close()
		return nil, errors.New("file changed during validation")
	}
	if e = rejectHardlink(f, after); e != nil {
		f.Close()
		return nil, e
	}
	return f, nil
}

// checkAncestors refuses existing symlinks/reparse points after normalizing
// only Darwin's verified /var and /tmp system mappings. Arbitrary aliases in
// parent directories are never resolved away.
func checkAncestors(p string) error {
	abs, e := filepath.Abs(p)
	if e != nil {
		return e
	}
	abs, e = normalizeSystemPath(abs)
	if e != nil {
		return e
	}
	for {
		info, e := os.Lstat(abs)
		if e == nil {
			if e = rejectLink(info); e != nil {
				return fmt.Errorf("unsafe path %s: %w", abs, e)
			}
		} else if !os.IsNotExist(e) {
			return e
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			break
		}
		abs = parent
	}
	return nil
}
