package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/gofrs/flock"
)

type Current struct {
	Version        string `json:"version"`
	OS             string `json:"os"`
	Arch           string `json:"arch"`
	ManifestSHA256 string `json:"manifest_sha256"`
	Dir            string `json:"dir"`
}
type Progress struct {
	Phase          string    `json:"phase"`
	Version        string    `json:"version,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
	Error          string    `json:"error,omitempty"`
	Previous       *Current  `json:"previous,omitempty"`
	Target         *Current  `json:"target,omitempty"`
	RecoveryNeeded bool      `json:"recovery_needed,omitempty"`
}
type Options struct {
	Root           string
	StateDir       string
	BundleDir      string
	BeforeActivate func(context.Context, Current) error
	Health         func(context.Context, Current) error
	// CleanupOldVersions removes owned obsolete payloads only after health is
	// committed. BeforeCleanup must reject running or referenced generations.
	CleanupOldVersions bool
	BeforeCleanup      func(context.Context, Current) error
	// Rollback is called after the previous pointer is restored, for example to
	// restart its service. A nil pointer means this was the first installation.
	Rollback func(context.Context, *Current) error
	Progress func(Progress)
}
type Result struct {
	Current  Current
	Previous *Current
	Reused   bool
	// CleanupPending leaves Current healthy and active; retry Apply or Repair.
	CleanupPending bool
}

// ErrCleanupPending means activation committed, but obsolete owned files still
// need cleanup. The caller must keep the new runtime active, not roll it back.
var ErrCleanupPending = errors.New("cleanup_pending")

type RemoveOptions struct {
	Root         string
	StateDir     string
	BeforeRemove func(context.Context) error
}
type ownedVersion struct {
	Dir      string   `json:"dir"`
	Hash     string   `json:"hash"`
	Manifest Manifest `json:"manifest"`
}
type ownership struct {
	Schema        int            `json:"schema"`
	Versions      []ownedVersion `json:"versions"`
	RetainedState string         `json:"retained_state,omitempty"`
	StateDir      string         `json:"state_dir,omitempty"`
}

const ownerName = ".dotcontrol-install.json"

// DefaultRoot keeps executable payloads separate from config.DefaultDir().
func DefaultRoot() (string, error) {
	if runtime.GOOS == "windows" {
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			return "", errors.New("LOCALAPPDATA is unset")
		}
		return filepath.Join(base, "DotControl"), nil
	}
	home, e := os.UserHomeDir()
	if e != nil {
		return "", e
	}
	return filepath.Join(home, "Library", "Application Support", "DotControl", "program"), nil
}

// ValidatePaths performs the same read-only namespace, ancestry, and containment
// checks used by Apply before callers create their state-level coordination lock.
func ValidatePaths(root, state string) error { _, err := preparePaths(root, state); return err }

// CanonicalPath reuses the installer's strict namespace/link checks, including
// only Darwin's verified system aliases. It does not create files. Callers must
// retain the original state spelling for existing runtime/vault namespaces.
func CanonicalPath(path string) (string, error) { return canonicalInstallPath(path) }

func preparePaths(root, state string) (string, error) {
	if root == "" || state == "" {
		return "", errors.New("install root and separate state directory are required")
	}
	abs, e := canonicalInstallPath(root)
	if e != nil {
		return "", e
	}
	state, e = canonicalInstallPath(state)
	if e != nil {
		return "", e
	}
	if filepath.Dir(abs) == abs {
		return "", errors.New("filesystem root cannot be an install root")
	}
	if within(abs, state) {
		return "", errors.New("install root must not equal or contain the state directory")
	}
	if e = checkAncestors(abs); e != nil {
		return "", e
	}
	if e = checkAncestors(state); e != nil {
		return "", e
	}
	return abs, nil
}
func canonicalInstallPath(p string) (string, error) {
	if e := rejectPathNamespace(p); e != nil {
		return "", e
	}
	abs, e := filepath.Abs(p)
	if e != nil {
		return "", e
	}
	abs, e = normalizeSystemPath(abs)
	if e != nil {
		return "", e
	}
	// Check the original spelling before resolving it, so resolving a short name
	// never hides an existing junction or symlink from the link prohibition.
	if e = checkAncestors(abs); e != nil {
		return "", e
	}
	return canonicalExistingPath(abs)
}
func within(root, p string) bool {
	// Case-fold on every platform so portable bundles cannot acquire aliases on
	// the usual case-insensitive macOS or Windows volume.
	root = strings.ToLower(filepath.Clean(root))
	p = strings.ToLower(filepath.Clean(p))
	rel, e := filepath.Rel(root, p)
	return e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
func lockRoot(ctx context.Context, root string) (func(), error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if e := os.MkdirAll(root, 0700); e != nil {
		return nil, e
	}
	p := filepath.Join(root, ".install.lock")
	if e := checkAncestors(p); e != nil {
		return nil, e
	}
	if _, e := os.Lstat(p); e == nil {
		if _, e = openAndCloseRegular(p); e != nil {
			return nil, e
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	l := flock.New(p)
	ok, e := l.TryLockContext(ctx, 25*time.Millisecond)
	if e != nil {
		return nil, e
	}
	if !ok {
		return nil, ctx.Err()
	}
	return func() { _ = l.Unlock(); _ = l.Close() }, nil
}
func readJSON(p string, v any) error {
	f, e := openRegular(p)
	if e != nil {
		return e
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, 32<<20))
	dec.DisallowUnknownFields()
	if e = dec.Decode(v); e != nil {
		return e
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return errors.New("invalid trailing JSON")
	}
	return nil
}
func atomicJSON(p string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	return atomicBytes(p, append(b, '\n'), 0600)
}
func atomicBytes(p string, b []byte, mode os.FileMode) error {
	if e := checkAncestors(p); e != nil {
		return e
	}
	if _, e := os.Lstat(p); e == nil {
		if _, e = openAndCloseRegular(p); e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(p), ".write-*")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(mode); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return e
	}
	return os.Rename(tmp, p)
}
func ReadProgress(root string) (Progress, error) {
	var p Progress
	e := readJSON(filepath.Join(root, "transaction.json"), &p)
	return p, e
}
func ReadCurrent(root string) (Current, error) {
	var c Current
	abs, e := canonicalInstallPath(root)
	if e != nil {
		return c, e
	}
	if e = readJSON(filepath.Join(abs, "current.json"), &c); e != nil {
		return c, e
	}
	if e = validateCurrent(abs, c); e != nil {
		return Current{}, e
	}
	return c, nil
}
func validateCurrent(root string, c Current) error {
	if !safeRelative(c.Version) || strings.Contains(c.Version, "/") || len(c.ManifestSHA256) != 64 || c.OS != runtime.GOOS || c.Arch != runtime.GOARCH {
		return errors.New("invalid current pointer")
	}
	if _, e := hex.DecodeString(c.ManifestSHA256); e != nil {
		return errors.New("invalid current manifest hash")
	}
	versions := filepath.Join(root, "versions")
	if !within(versions, c.Dir) || filepath.Dir(filepath.Clean(c.Dir)) != versions {
		return errors.New("current pointer escapes versions directory")
	}
	return checkAncestors(c.Dir)
}
func loadOwnership(root string) (ownership, error) {
	own := ownership{Schema: 1}
	e := readJSON(filepath.Join(root, ownerName), &own)
	if os.IsNotExist(e) {
		for _, p := range []string{"current.json", "versions", "transaction.json"} {
			if _, other := os.Lstat(filepath.Join(root, p)); other == nil {
				return own, errors.New("existing installation has no ownership record")
			} else if !os.IsNotExist(other) {
				return own, other
			}
		}
		return own, nil
	}
	if e != nil {
		return own, e
	}
	if own.Schema != 1 {
		return own, errors.New("unsupported install ownership schema")
	}
	if (own.StateDir != "" && !filepath.IsAbs(own.StateDir)) || (own.RetainedState != "" && (own.StateDir == "" || own.RetainedState != own.StateDir)) {
		return own, errors.New("invalid managed state binding")
	}
	seen := map[string]bool{}
	for _, v := range own.Versions {
		if !safeRelative(v.Dir) || strings.Contains(v.Dir, "/") || seen[strings.ToLower(v.Dir)] {
			return own, errors.New("invalid owned directory")
		}
		seen[strings.ToLower(v.Dir)] = true
		if e = validateManifest(v.Manifest); e != nil {
			return own, e
		}
		if manifestHash(v.Manifest) != v.Hash {
			return own, errors.New("invalid ownership manifest hash")
		}
	}
	return own, nil
}

// RetainedStateMatches proves that a prior completed managed uninstall retained this
// exact state directory. Empty/older ownership records are not such a proof.
func RetainedStateMatches(root, state string) (bool, error) {
	root, e := preparePaths(root, state)
	if e != nil {
		return false, e
	}
	own, e := loadOwnership(root)
	if e != nil || own.RetainedState == "" {
		return false, e
	}
	saved, e := canonicalInstallPath(own.RetainedState)
	if e != nil {
		return false, e
	}
	state, e = canonicalInstallPath(state)
	return e == nil && sameManagedPath(saved, state), e
}

// An exact canonical path or filesystem identity can grant ownership. Case
// folding alone cannot: even Windows directories may be case-sensitive.
func sameManagedPath(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ai, ae := os.Stat(a)
	bi, be := os.Stat(b)
	return ae == nil && be == nil && os.SameFile(ai, bi)
}
func validateManagedState(own ownership, state string) error {
	if own.StateDir == "" {
		return nil
	} // Older installs acquire a binding after health succeeds.
	saved, e := canonicalInstallPath(own.StateDir)
	if e != nil {
		return e
	}
	selected, e := canonicalInstallPath(state)
	if e != nil {
		return e
	}
	if !sameManagedPath(saved, selected) {
		return errors.New("install_state_binding_mismatch")
	}
	return nil
}

// OwnedFile validates a current pointer against its recorded installation
// manifest, even if a payload was removed by quarantine or interrupted cleanup.
func OwnedFile(root string, current Current, relative string) (File, error) {
	root, e := canonicalInstallPath(root)
	if e != nil {
		return File{}, e
	}
	if e = validateCurrent(root, current); e != nil {
		return File{}, e
	}
	own, e := loadOwnership(root)
	if e != nil {
		return File{}, e
	}
	for _, v := range own.Versions {
		recorded, err := canonicalInstallPath(filepath.Join(root, "versions", v.Dir))
		if err != nil {
			return File{}, err
		}
		if v.Hash == current.ManifestSHA256 && sameManagedPath(recorded, current.Dir) && v.Manifest.Version == current.Version {
			for _, f := range v.Manifest.Files {
				if f.Path == relative {
					return f, nil
				}
			}
		}
	}
	return File{}, errors.New("previous_program_not_owned")
}
func restore(root string, previous *Current) error {
	if previous != nil {
		if e := validateCurrent(root, *previous); e != nil {
			return e
		}
		return atomicJSON(filepath.Join(root, "current.json"), previous)
	}
	p := filepath.Join(root, "current.json")
	if e := checkAncestors(p); e != nil {
		return e
	}
	if e := os.Remove(p); !os.IsNotExist(e) {
		return e
	}
	return nil
}
func rollbackActivation(ctx context.Context, root string, previous *Current, callback func(context.Context, *Current) error) error {
	if e := restore(root, previous); e != nil {
		return e
	}
	if callback != nil {
		return callback(context.WithoutCancel(ctx), previous)
	}
	return nil
}

func recoverInterrupted(ctx context.Context, root string, callback func(context.Context, *Current) error) error {
	p, e := ReadProgress(root)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if !p.RecoveryNeeded {
		return nil
	}
	e = rollbackActivation(ctx, root, p.Previous, callback)
	p.RecoveryNeeded = e != nil
	p.Phase = "failed"
	p.Error = "interrupted activation rolled back"
	if e != nil {
		p.Error = "interrupted activation rollback failed: " + e.Error()
	}
	p.UpdatedAt = time.Now().UTC()
	return errors.Join(e, atomicJSON(filepath.Join(root, "transaction.json"), p))
}

// Apply verifies a complete local bundle, resumes verified staging, and changes
// current.json only after the immutable version has been fully installed.
// It holds a per-root cross-process lock through the health callback.
func Apply(ctx context.Context, o Options) (result Result, err error) { return apply(ctx, o, false) }

// Repair builds a fresh immutable generation if the existing one is damaged.
func Repair(ctx context.Context, o Options) (result Result, err error) { return apply(ctx, o, true) }
func apply(ctx context.Context, o Options, repair bool) (result Result, err error) {
	root, e := preparePaths(o.Root, o.StateDir)
	if e != nil {
		return result, e
	}
	unlock, e := lockRoot(ctx, root)
	if e != nil {
		return result, e
	}
	defer unlock()
	own, e := loadOwnership(root)
	if e != nil {
		return result, e
	}
	if e = validateManagedState(own, o.StateDir); e != nil {
		return result, e
	}
	if e = atomicJSON(filepath.Join(root, ownerName), own); e != nil {
		return result, e
	}
	if e = recoverInterrupted(ctx, root, o.Rollback); e != nil {
		return result, e
	}
	p := Progress{}
	committed := false
	report := func(phase string) error {
		p.Phase = phase
		p.UpdatedAt = time.Now().UTC()
		if e := atomicJSON(filepath.Join(root, "transaction.json"), p); e != nil {
			return e
		}
		if phase == "cleaning_old_versions" {
			committed = true
		}
		if o.Progress != nil {
			o.Progress(p)
		}
		return ctx.Err()
	}
	defer func() {
		if err != nil {
			if committed {
				result.CleanupPending = true
				p.RecoveryNeeded = false
				err = errors.Join(ErrCleanupPending, err)
				p.Phase = "cleanup_pending"
			} else {
				if p.RecoveryNeeded {
					rollbackErr := rollbackActivation(ctx, root, result.Previous, o.Rollback)
					p.RecoveryNeeded = rollbackErr != nil
					err = errors.Join(err, rollbackErr)
				}
				result.Current = Current{}
				p.Phase = "failed"
			}
			p.Error = err.Error()
			p.UpdatedAt = time.Now().UTC()
			if e := atomicJSON(filepath.Join(root, "transaction.json"), p); e != nil {
				err = errors.Join(err, e)
			}
			if o.Progress != nil {
				o.Progress(p)
			}
		}
	}()
	// A committed cleanup is irreversible: resume it before inspecting a new
	// bundle, and never feed its previous (possibly partial) payload to rollback.
	prior, pe := ReadProgress(root)
	if pe != nil && !os.IsNotExist(pe) {
		return result, pe
	}
	if pe == nil && (prior.Phase == "cleaning_old_versions" || prior.Phase == "cleanup_pending") {
		p = prior
		committed = true
		if p.Target == nil || p.RecoveryNeeded {
			return result, errors.New("invalid committed cleanup journal")
		}
		result = Result{Current: *p.Target, Previous: p.Previous, Reused: true}
		if err = cleanupCurrentMatches(root, result.Current); err != nil {
			return result, err
		}
		p.Error = ""
		if err = report("cleaning_old_versions"); err != nil {
			return result, err
		}
		if err = cleanupOldVersions(ctx, root, &own, result.Current, o.BeforeCleanup); err != nil {
			return result, err
		}
		if err = report("complete"); err != nil {
			return result, err
		}
		if o.BundleDir == "" {
			return result, nil
		}
		p, result, committed = Progress{}, Result{}, false
	}
	if o.CleanupOldVersions && o.Health == nil {
		return result, errors.New("cleanup requires a health check")
	}
	if err = report("download"); err != nil {
		return result, err
	}
	if o.BundleDir == "" {
		return result, errors.New("a complete local bundle is required")
	}
	if err = report("verify"); err != nil {
		return result, err
	}
	m, e := Verify(ctx, o.BundleDir)
	if e != nil {
		return result, e
	}
	digest := manifestHash(m)
	p.Version = m.Version
	for _, v := range own.Versions {
		if strings.EqualFold(v.Manifest.Version, m.Version) && v.Hash != digest {
			return result, errors.New("version already exists with different manifest bytes")
		}
	}
	current, e := ReadCurrent(root)
	if e == nil {
		result.Previous = &current
		p.Previous = &current
	} else if !os.IsNotExist(e) {
		return result, e
	}
	base := m.Version + "-" + digest[:16]
	dest := filepath.Join(root, "versions", base)
	if e = checkAncestors(dest); e != nil {
		return result, e
	}
	if _, e = os.Lstat(dest); e == nil {
		existing, ve := Verify(ctx, dest)
		if ve == nil && manifestHash(existing) == digest {
			result.Reused = true
		} else {
			if !repair {
				return result, fmt.Errorf("installed version is damaged; use repair: %w", ve)
			}
			base = base + "-repair-" + fmt.Sprint(time.Now().UnixNano())
			dest = filepath.Join(root, "versions", base)
		}
	} else if !os.IsNotExist(e) {
		return result, e
	}
	if err = report("stage"); err != nil {
		return result, err
	}
	stage := filepath.Join(root, ".staging", digest)
	if !result.Reused {
		if e = stageBundle(ctx, o.BundleDir, stage, m); e != nil {
			return result, e
		}
	}
	target := Current{Version: m.Version, OS: m.OS, Arch: m.Arch, ManifestSHA256: digest, Dir: dest}
	p.Target = &target
	if err = report("install"); err != nil {
		return result, err
	}
	if !result.Reused {
		if e = checkAncestors(filepath.Dir(dest)); e != nil {
			return result, e
		}
		if e = os.MkdirAll(filepath.Dir(dest), 0700); e != nil {
			return result, e
		}
		exists := false
		for _, v := range own.Versions {
			if v.Dir == base {
				exists = true
			}
		}
		if !exists {
			own.Versions = append(own.Versions, ownedVersion{Dir: base, Hash: digest, Manifest: m})
			if e = atomicJSON(filepath.Join(root, ownerName), own); e != nil {
				return result, e
			}
		}
		if e = os.Rename(stage, dest); e != nil {
			return result, e
		}
	}
	if e = ctx.Err(); e != nil {
		return result, e
	}
	// Journal the intent before a callback can stop or partially reconfigure the
	// old runtime. A crash in BeforeActivate needs service recovery as well.
	p.RecoveryNeeded = true
	p.UpdatedAt = time.Now().UTC()
	if e = atomicJSON(filepath.Join(root, "transaction.json"), p); e != nil {
		p.RecoveryNeeded = false
		return result, e
	}
	if o.BeforeActivate != nil || o.CleanupOldVersions {
		if err = report("waiting_for_exit"); err != nil {
			return result, err
		}
	}
	if o.BeforeActivate != nil {
		if e = o.BeforeActivate(ctx, target); e != nil {
			return result, e
		}
	}
	if err = report("health"); err != nil {
		return result, err
	}
	if e = atomicJSON(filepath.Join(root, "current.json"), target); e == nil && o.Health != nil {
		e = o.Health(ctx, target)
	}
	if e == nil {
		e = ctx.Err()
	}
	if e != nil {
		return result, e
	}
	// Only a verified activation establishes state provenance. Staging or a
	// failed install must not mint a retained-state reinstall authorization.
	bound, e := canonicalInstallPath(o.StateDir)
	if e != nil {
		return result, e
	}
	if own.StateDir == "" {
		own.StateDir = bound
	}
	// A prior uninstall receipt is provenance, not a one-shot token. Keep it
	// through activation/rollback so a crash before commit cannot strand a
	// retained configuration with no previous executable to restart.
	if e = atomicJSON(filepath.Join(root, ownerName), own); e != nil {
		return result, e
	}
	p.RecoveryNeeded = false
	result.Current = target
	if o.CleanupOldVersions {
		if err = report("cleaning_old_versions"); err != nil {
			if !committed {
				p.RecoveryNeeded = true
			}
			return result, err
		}
		if err = cleanupOldVersions(ctx, root, &own, target, o.BeforeCleanup); err != nil {
			return result, err
		}
	}
	if err = report("complete"); err != nil {
		p.RecoveryNeeded = !committed
		return result, err
	}
	return result, nil
}
func stageBundle(ctx context.Context, source, stage string, m Manifest) error {
	if e := checkAncestors(stage); e != nil {
		return e
	}
	if e := os.MkdirAll(stage, 0700); e != nil {
		return e
	}
	// An existing stage must not contain unlisted objects. Corrupt listed regular
	// files can be replaced from the verified source; links always fail closed.
	allowed := map[string]bool{".": true, "manifest.json": true}
	for _, f := range m.Files {
		p := filepath.FromSlash(f.Path)
		allowed[p] = true
		for parent := filepath.Dir(p); parent != "."; parent = filepath.Dir(parent) {
			allowed[parent] = true
		}
	}
	if e := filepath.Walk(stage, func(p string, info os.FileInfo, e error) error {
		if e != nil {
			return e
		}
		if e = rejectLink(info); e != nil {
			return e
		}
		rel, _ := filepath.Rel(stage, p)
		if !allowed[rel] {
			return fmt.Errorf("unexpected staged path: %s", rel)
		}
		if !info.IsDir() {
			_, e = openAndCloseRegular(p)
			return e
		}
		return nil
	}); e != nil {
		return e
	}
	for _, spec := range m.Files {
		if e := ctx.Err(); e != nil {
			return e
		}
		dest := filepath.Join(stage, filepath.FromSlash(spec.Path))
		if e := verifyFile(ctx, dest, spec); e == nil {
			continue
		}
		if e := checkAncestors(dest); e != nil {
			return e
		}
		if e := os.MkdirAll(filepath.Dir(dest), 0755); e != nil {
			return e
		}
		if e := copyVerified(ctx, filepath.Join(source, filepath.FromSlash(spec.Path)), dest, filepath.Dir(stage), spec); e != nil {
			return e
		}
	}
	if e := atomicJSON(filepath.Join(stage, "manifest.json"), m); e != nil {
		return e
	}
	_, e := Verify(ctx, stage)
	return e
}
func copyVerified(ctx context.Context, source, dest, scratch string, spec File) error {
	in, e := openRegular(source)
	if e != nil {
		return e
	}
	defer in.Close()
	out, e := os.CreateTemp(scratch, ".part-*")
	if e != nil {
		return e
	}
	tmp := out.Name()
	defer os.Remove(tmp)
	h := sha256.New()
	_, e = io.Copy(io.MultiWriter(out, h), &contextReader{ctx: ctx, r: in})
	if e == nil && hex.EncodeToString(h.Sum(nil)) != spec.SHA256 {
		e = errors.New("source changed while staging")
	}
	if e == nil {
		e = out.Chmod(os.FileMode(spec.Mode))
	}
	if e == nil {
		e = out.Sync()
	}
	ce := out.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	if e = checkAncestors(dest); e != nil {
		return e
	}
	if _, e = os.Lstat(dest); e == nil {
		if _, e = openAndCloseRegular(dest); e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	return os.Rename(tmp, dest)
}

// Remove deletes only paths recorded in the install ownership record. State,
// credentials, unlisted files, and old running processes are never touched.
// The service callback must stop any running owned programs before removal.
func Remove(ctx context.Context, o RemoveOptions) error {
	root, e := preparePaths(o.Root, o.StateDir)
	if e != nil {
		return e
	}
	if _, e = os.Lstat(root); os.IsNotExist(e) {
		return nil
	} else if e != nil {
		return e
	}
	unlock, e := lockRoot(ctx, root)
	if e != nil {
		return e
	}
	defer unlock()
	own, e := loadOwnership(root)
	if e != nil {
		return e
	}
	if e = validateManagedState(own, o.StateDir); e != nil {
		return e
	}
	if o.BeforeRemove != nil {
		if e = o.BeforeRemove(ctx); e != nil {
			return e
		}
	}
	if e = recoverInterrupted(ctx, root, nil); e != nil {
		return e
	}
	for _, v := range own.Versions {
		dir := filepath.Join(root, "versions", v.Dir)
		if e = checkAncestors(dir); e != nil {
			return e
		}
		for _, f := range v.Manifest.Files {
			if e = ctx.Err(); e != nil {
				return e
			}
			p := filepath.Join(dir, filepath.FromSlash(f.Path))
			if _, e = os.Lstat(p); os.IsNotExist(e) {
				continue
			} else if e != nil {
				return e
			}
			if _, e = openAndCloseRegular(p); e != nil {
				return e
			}
			if e = os.Remove(p); e != nil {
				return e
			}
			for parent := filepath.Dir(p); parent != dir && within(dir, parent); parent = filepath.Dir(parent) {
				_ = os.Remove(parent)
			}
		}
		p := filepath.Join(dir, "manifest.json")
		if _, e = os.Lstat(p); e == nil {
			if _, e = openAndCloseRegular(p); e != nil {
				return e
			}
			if e = os.Remove(p); e != nil {
				return e
			}
		} else if !os.IsNotExist(e) {
			return e
		}
		_ = os.Remove(dir)
	}
	for _, name := range []string{"current.json", "transaction.json"} {
		p := filepath.Join(root, name)
		if _, e = os.Lstat(p); os.IsNotExist(e) {
			continue
		} else if e != nil {
			return e
		}
		if _, e = openAndCloseRegular(p); e != nil {
			return e
		}
		if e = os.Remove(p); e != nil {
			return e
		}
	}
	_ = os.Remove(filepath.Join(root, "versions"))
	// Keep an empty ownership marker alongside the lock. User-created files can
	// legitimately keep a version directory present after uninstall; retaining
	// this marker makes uninstall idempotent without claiming those files.
	return atomicJSON(filepath.Join(root, ownerName), ownership{Schema: 1, StateDir: own.StateDir, RetainedState: own.StateDir})
}
