package dotevents

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"time"

	"github.com/gofrs/flock"
)

type store struct {
	dir string
	key []byte
	// beforeLock permits deterministic lock-contention tests; nil in production.
	beforeLock func()
}

func openStore(dir string, key []byte) (*store, error) {
	if len(key) != 32 {
		return nil, errf("unknown")
	}
	if validateStorePath(dir) != nil {
		return nil, errf("unknown")
	}
	return &store{dir: dir, key: append([]byte(nil), key...)}, nil
}

func (s *store) withLock(allowCreate bool, mutate bool, fn func(*persistenceState) error) error {
	if err := s.checkDir(allowCreate); err != nil {
		return err
	}
	lockPath := filepath.Join(s.dir, lockName)
	if err := regularFile(lockPath); err != nil {
		return wrapUnknown(err)
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return errf("unknown")
	}
	info, statErr := f.Stat()
	if statErr != nil || rejectHardlink(f, info) != nil {
		f.Close()
		return errf("unknown")
	}
	if f.Close() != nil || privatePath(lockPath, false) != nil {
		return errf("unknown")
	}
	lock := flock.New(lockPath)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if s.beforeLock != nil {
		s.beforeLock()
	}
	ok, err := lock.TryLockContext(ctx, 10*time.Millisecond)
	if err != nil || !ok {
		return errf("unknown")
	}
	defer lock.Unlock()
	if e := s.checkDir(false); e != nil {
		return e
	}
	for _, name := range []string{stateFileName, markerName, lockName} {
		if e := protectFile(filepath.Join(s.dir, name), false); e != nil {
			return e
		}
	}

	st, err := s.loadState(allowCreate)
	if err != nil {
		return err
	}
	if st == nil {
		st = newPersistenceState()
	}
	if mutate {
		if err := fn(st); err != nil {
			return err
		}
		return s.saveState(st)
	}
	return fn(st)
}

func (s *store) loadState(allowCreate bool) (*persistenceState, error) {
	marker := filepath.Join(s.dir, markerName)
	_, markerErr := os.Lstat(marker)
	statePath := filepath.Join(s.dir, stateFileName)
	_, stateErr := os.Lstat(statePath)
	if os.IsNotExist(markerErr) && os.IsNotExist(stateErr) {
		if allowCreate {
			return nil, nil
		}
		return nil, errf("corrupt")
	}
	if markerErr != nil || stateErr != nil {
		if os.IsNotExist(stateErr) && markerErr == nil {
			return nil, errf("corrupt")
		}
		return nil, errf("corrupt")
	}
	markerFile, e := os.Open(marker)
	if e != nil {
		return nil, errf("corrupt")
	}
	markerData, e := io.ReadAll(io.LimitReader(markerFile, 3))
	closeErr := markerFile.Close()
	if e != nil || closeErr != nil || string(markerData) != "1\n" {
		return nil, errf("corrupt")
	}
	if err := regularFile(statePath); err != nil {
		return nil, errf("corrupt")
	}
	f, err := os.Open(statePath)
	if err != nil {
		return nil, errf("corrupt")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || rejectHardlink(f, info) != nil {
		return nil, errf("corrupt")
	}
	if info.Size() > maxStateBytes {
		return nil, errf("corrupt")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxStateBytes+1))
	if err != nil {
		return nil, errf("corrupt")
	}
	plain, err := openEnvelope(s.key, data)
	if err != nil {
		return nil, err
	}
	return parsePersistenceState(plain)
}

func (s *store) saveState(st *persistenceState) error {
	if st.StateVersion != stateVersion {
		return errf("corrupt")
	}
	data, err := jsonMarshal(st)
	if err != nil || len(data) > maxStateBytes {
		return errf("unknown")
	}
	sealed, err := seal(s.key, data)
	if err != nil {
		return err
	}
	statePath := filepath.Join(s.dir, stateFileName)
	if err := regularFile(statePath); err != nil {
		return errf("unknown")
	}
	f, err := os.CreateTemp(s.dir, ".dot-events-*")
	if err != nil {
		return errf("unknown")
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if privatePath(tmp, false) != nil {
		f.Close()
		return errf("unknown")
	}
	n, writeErr := f.Write(sealed)
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil || n != len(sealed) || syncErr != nil || closeErr != nil {
		return errf("unknown")
	}
	if regularFile(statePath) != nil || replaceFile(tmp, statePath) != nil || syncDirectory(s.dir) != nil {
		return errf("unknown")
	}
	return s.ensureMarker()
}

func (s *store) ensureMarker() error {
	path := filepath.Join(s.dir, markerName)
	if err := regularFile(path); err != nil {
		return errf("unknown")
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(err) {
		f, err = os.Open(path)
		if err != nil {
			return errf("unknown")
		}
		info, statErr := f.Stat()
		if statErr != nil || rejectHardlink(f, info) != nil {
			f.Close()
			return errf("unknown")
		}
		if f.Close() != nil || privatePath(path, false) != nil {
			return errf("unknown")
		}
		return nil
	}
	if err != nil {
		return errf("unknown")
	}
	if privatePath(path, false) != nil {
		f.Close()
		return errf("unknown")
	}
	_, writeErr := f.WriteString("1\n")
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil || syncDirectory(s.dir) != nil {
		return errf("unknown")
	}
	return nil
}

func (s *store) checkDir(create bool) error { return checkAncestors(s.dir, create) }

func regularFile(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || rejectLink(info) != nil {
		return errf("unknown")
	}
	return nil
}

func jsonMarshal(v any) ([]byte, error) {
	return json.Marshal(v)
}
