package historyaccess

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/gofrs/flock"
)

const maxPolicyBytes = 1024 * 1024
const policyName = "history_access.json"
const markerName = "history_access.initialized"

// Store requires a local absolute state directory with trusted parents. Files
// receive a current-user-only Windows ACL, or mode 0600 under a 0700 directory.
type Store struct{ dir string }

func NewStore(dir string) *Store { return &Store{dir: dir} }

func (s *Store) checkDir(create bool) error {
	if !filepath.IsAbs(s.dir) || len(s.dir) > 32768 || strings.ContainsFunc(s.dir, unicode.IsControl) {
		return failure("invalid_state_dir")
	}
	if create {
		if os.MkdirAll(s.dir, 0700) != nil {
			return failure("storage_unavailable")
		}
	}
	info, err := os.Lstat(s.dir)
	if os.IsNotExist(err) {
		return failure("access_missing")
	}
	if err != nil || !info.IsDir() || rejectLink(info) != nil {
		return failure("storage_unavailable")
	}
	if create && privatePath(s.dir, true) != nil {
		return failure("storage_unavailable")
	}
	return nil
}

func regularFile(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || rejectLink(info) != nil {
		return failure("storage_unavailable")
	}
	return nil
}

func (s *Store) locked(create bool, operation func() (Policy, error)) (Policy, error) {
	if err := s.checkDir(create); err != nil {
		return Policy{}, err
	}
	path := filepath.Join(s.dir, "history_access.lock")
	if err := regularFile(path); err != nil {
		return Policy{}, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return Policy{}, failure("storage_unavailable")
	}
	info, statErr := f.Stat()
	if statErr != nil || rejectHardlink(f, info) != nil {
		f.Close()
		return Policy{}, failure("storage_unavailable")
	}
	if f.Close() != nil || privatePath(path, false) != nil {
		return Policy{}, failure("storage_unavailable")
	}
	lock := flock.New(path)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ok, err := lock.TryLockContext(ctx, 10*time.Millisecond)
	if err != nil || !ok {
		return Policy{}, failure("locked")
	}
	defer lock.Unlock()
	return operation()
}

// Load never initializes a missing policy. All readers and writers use the
// same lock, including on Windows where an open reader can prevent replacement.
func (s *Store) Load() (Policy, error) { return s.locked(false, s.load) }

func (s *Store) load() (Policy, error) {
	path := filepath.Join(s.dir, policyName)
	if err := regularFile(path); err != nil {
		return Policy{}, err
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return Policy{}, failure("access_missing")
	}
	if err != nil {
		return Policy{}, failure("storage_unavailable")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || rejectHardlink(f, info) != nil {
		return Policy{}, failure("storage_unavailable")
	}
	if info.Size() > maxPolicyBytes {
		return Policy{}, failure("invalid_policy")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxPolicyBytes+1))
	if err != nil {
		return Policy{}, failure("storage_unavailable")
	}
	obj, err := decodeObject(data, maxPolicyBytes)
	if err != nil || len(obj) != 4 {
		return Policy{}, failure("invalid_policy")
	}
	for _, key := range []string{"version", "mode", "revision", "allowed_thread_ids"} {
		raw, ok := obj[key]
		if !ok || string(raw) == "null" {
			return Policy{}, failure("invalid_policy")
		}
	}
	var p Policy
	if json.Unmarshal(data, &p) != nil {
		return Policy{}, failure("invalid_policy")
	}
	return normalized(p)
}

// InitializeAll is reserved for explicit first enable. A durable marker stops
// later missing-file recovery from silently restoring broad access. A crash
// between the marker and first policy write also fails closed.
func (s *Store) InitializeAll() (Policy, error) {
	return s.locked(true, func() (Policy, error) {
		p, err := s.load()
		if err == nil {
			if err = s.ensureMarker(); err != nil {
				return Policy{}, err
			}
			return p, nil
		}
		if err.Error() != "history_access_missing" {
			return Policy{}, err
		}
		if _, e := os.Lstat(filepath.Join(s.dir, markerName)); !os.IsNotExist(e) {
			if e != nil {
				return Policy{}, failure("storage_unavailable")
			}
			return Policy{}, failure("access_missing")
		}
		if err = s.ensureMarker(); err != nil {
			return Policy{}, err
		}
		p = Policy{Version: 1, Mode: "all", Revision: 1, AllowedThreadIDs: []string{}}
		if err = s.save(p); err != nil {
			return Policy{}, err
		}
		return p, nil
	})
}

func (s *Store) ensureMarker() error {
	path := filepath.Join(s.dir, markerName)
	if err := regularFile(path); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(err) {
		f, err = os.Open(path)
		if err != nil {
			return failure("storage_unavailable")
		}
		info, statErr := f.Stat()
		if statErr != nil || rejectHardlink(f, info) != nil {
			f.Close()
			return failure("storage_unavailable")
		}
		if f.Close() != nil || privatePath(path, false) != nil {
			return failure("storage_unavailable")
		}
		return nil
	}
	if err != nil {
		return failure("storage_unavailable")
	}
	if privatePath(path, false) != nil {
		f.Close()
		return failure("storage_unavailable")
	}
	_, writeErr := f.WriteString("1\n")
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil || syncDirectory(s.dir) != nil {
		return failure("storage_unavailable")
	}
	return nil
}

// Apply atomically compares the revision and replaces the complete policy.
// Empty allowlists deny every thread; mode all cannot carry a hidden allowlist.
func (s *Store) Apply(expectedRevision uint64, mode string, ids []string) (Policy, error) {
	return s.locked(false, func() (Policy, error) {
		p, err := s.load()
		if err != nil {
			return Policy{}, err
		}
		if expectedRevision != p.Revision {
			return Policy{}, failure("revision_conflict")
		}
		if p.Revision == math.MaxUint64 {
			return Policy{}, failure("revision_exhausted")
		}
		p, err = normalized(Policy{Version: 1, Mode: mode, Revision: p.Revision + 1, AllowedThreadIDs: ids})
		if err != nil {
			return Policy{}, err
		}
		if err = s.ensureMarker(); err != nil {
			return Policy{}, err
		}
		if err = s.save(p); err != nil {
			return Policy{}, err
		}
		return p, nil
	})
}

func (s *Store) save(p Policy) error {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil || len(data) >= maxPolicyBytes {
		return failure("invalid_policy")
	}
	data = append(data, '\n')
	f, err := os.CreateTemp(s.dir, ".history-access-*")
	if err != nil {
		return failure("storage_unavailable")
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if privatePath(tmp, false) != nil {
		f.Close()
		return failure("storage_unavailable")
	}
	n, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil || n != len(data) || syncErr != nil || closeErr != nil {
		return failure("storage_unavailable")
	}
	target := filepath.Join(s.dir, policyName)
	if regularFile(target) != nil || replaceFile(tmp, target) != nil || syncDirectory(s.dir) != nil {
		return failure("storage_unavailable")
	}
	return nil
}
