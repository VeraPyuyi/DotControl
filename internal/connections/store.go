package connections

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"
)

const maxConfigBytes = 1024 * 1024

// Store uses a dedicated local state directory with a current-user-only ACL on
// Windows and mode 0700 elsewhere. Parent directories must be trusted.
type Store struct{ dir string }

func NewStore(dir string) *Store { return &Store{dir: dir} }

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

func (s *Store) prepare() error {
	if !absoluteLocal(s.dir) {
		return failure("invalid_state_dir")
	}
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return failure("storage_unavailable")
	}
	info, err := os.Lstat(s.dir)
	if err != nil || !info.IsDir() || rejectLink(info) != nil {
		return failure("storage_unavailable")
	}
	if privatePath(s.dir, true) != nil {
		return failure("storage_unavailable")
	}
	return nil
}

func (s *Store) Load() (Config, error) {
	// Reads do not create directories or rewrite configuration.
	if !absoluteLocal(s.dir) {
		return Config{}, failure("invalid_state_dir")
	}
	if info, err := os.Lstat(s.dir); err == nil {
		if !info.IsDir() || rejectLink(info) != nil {
			return Config{}, failure("storage_unavailable")
		}
	} else if !os.IsNotExist(err) {
		return Config{}, failure("storage_unavailable")
	}
	path := filepath.Join(s.dir, "connections.json")
	if err := regularFile(path); err != nil {
		return Config{}, err
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return Config{Version: SchemaVersion}, nil
	}
	if err != nil {
		return Config{}, failure("storage_unavailable")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxConfigBytes || rejectHardlink(f, info) != nil {
		return Config{}, failure("storage_unavailable")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxConfigBytes+1))
	if err != nil {
		return Config{}, failure("storage_unavailable")
	}
	if len(data) > maxConfigBytes {
		return Config{}, failure("invalid_connections_config")
	}
	var c Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(&c) != nil || dec.Decode(new(any)) != io.EOF {
		return Config{}, failure("invalid_connections_config")
	}
	if err = c.Validate(); err != nil {
		return Config{}, err
	}
	c.Normalize()
	return c, nil
}

// Update performs a locked read-modify-write. The callback must not reenter this
// store; errors from it are reduced to a fixed code to avoid leaking inputs.
func (s *Store) Update(change func(*Config) error) error {
	if change == nil {
		return failure("invalid_update")
	}
	if err := s.prepare(); err != nil {
		return err
	}
	lockPath := filepath.Join(s.dir, "connections.lock")
	if err := regularFile(lockPath); err != nil {
		return err
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return failure("storage_unavailable")
	}
	info, statErr := f.Stat()
	if statErr != nil || rejectHardlink(f, info) != nil {
		f.Close()
		return failure("storage_unavailable")
	}
	if f.Close() != nil || privatePath(lockPath, false) != nil {
		return failure("storage_unavailable")
	}
	lock := flock.New(lockPath)
	ok, err := lock.TryLock()
	if err != nil || !ok {
		return failure("connections_locked")
	}
	defer lock.Unlock()
	c, err := s.Load()
	if err != nil {
		return err
	}
	if err = change(&c); err != nil {
		return failure("update_rejected")
	}
	c.Normalize()
	if err = c.Validate(); err != nil {
		return err
	}
	return s.save(c)
}

func (s *Store) save(c Config) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil || len(data) >= maxConfigBytes {
		return failure("invalid_connections_config")
	}
	f, err := os.CreateTemp(s.dir, ".connections-*")
	if err != nil {
		return failure("storage_unavailable")
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if privatePath(tmp, false) != nil {
		f.Close()
		return failure("storage_unavailable")
	}
	data = append(data, '\n')
	n, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil || n != len(data) || syncErr != nil || closeErr != nil {
		return failure("storage_unavailable")
	}
	target := filepath.Join(s.dir, "connections.json")
	if regularFile(target) != nil || replaceFile(tmp, target) != nil || syncDirectory(s.dir) != nil {
		return failure("storage_unavailable")
	}
	return nil
}

func (s *Store) SetCodexExecutable(path string) error {
	return s.Update(func(c *Config) error { c.CodexExecutable = path; return nil })
}

func (s *Store) UpsertExecution(p ExecutionProfile) (ExecutionProfile, error) {
	if p.ID == "" {
		id, err := newID("exec_")
		if err != nil {
			return ExecutionProfile{}, err
		}
		p.ID = id
	}
	if p.Presentation == "" {
		p.Presentation = "visible"
	}
	err := s.Update(func(c *Config) error {
		for i := range c.ExecutionProfiles {
			if c.ExecutionProfiles[i].ID == p.ID {
				c.ExecutionProfiles[i] = p
				return nil
			}
		}
		c.ExecutionProfiles = append(c.ExecutionProfiles, p)
		return nil
	})
	if err != nil {
		return ExecutionProfile{}, err
	}
	return p, nil
}

// UpsertServer is a compatibility tombstone; legacy profiles are read-only.
func (s *Store) UpsertServer(ServerProfile) (ServerProfile, error) {
	return ServerProfile{}, failure("feature_retired")
}
