package experiment

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
)

func (m *Manager) statePath(id string) (string, error) {
	return m.recordPath(id, ".json")
}
func (m *Manager) recordPath(id, suffix string) (string, error) {
	if !filepath.IsAbs(m.dir) {
		return "", failure("experiment_storage_unavailable")
	}
	// Reject every existing symlink/reparse ancestor before making private state.
	current := filepath.Clean(m.dir)
	for {
		if info, e := os.Lstat(current); e == nil {
			if !info.IsDir() || rejectLink(info) != nil {
				return "", failure("experiment_storage_unavailable")
			}
		} else if !os.IsNotExist(e) {
			return "", failure("experiment_storage_unavailable")
		}
		next := filepath.Dir(current)
		if next == current {
			break
		}
		current = next
	}
	dir := filepath.Join(m.dir, "experiments")
	if info, e := os.Lstat(dir); e == nil {
		if !info.IsDir() || rejectLink(info) != nil {
			return "", failure("experiment_storage_unavailable")
		}
	} else if !os.IsNotExist(e) {
		return "", failure("experiment_storage_unavailable")
	}
	return filepath.Join(dir, id+suffix), nil
}
func (m *Manager) readIntent(id string) (intent, bool, error) {
	return m.readRecord(id, ".json")
}
func (m *Manager) readRecord(id, suffix string) (intent, bool, error) {
	var i intent
	file, e := m.recordPath(id, suffix)
	if e != nil {
		return i, false, e
	}
	info, e := os.Lstat(file)
	if os.IsNotExist(e) {
		return i, false, nil
	}
	if e != nil || !info.Mode().IsRegular() || rejectLink(info) != nil || info.Size() > 16384 {
		return i, false, failure("experiment_storage_unavailable")
	}
	f, e := os.Open(file)
	if e != nil {
		return i, false, failure("experiment_storage_unavailable")
	}
	defer f.Close()
	opened, e := f.Stat()
	if e != nil || !os.SameFile(info, opened) || rejectHardlink(f, opened) != nil {
		return i, false, failure("experiment_storage_unavailable")
	}
	b, e := io.ReadAll(io.LimitReader(f, 16385))
	if e != nil || len(b) > 16384 {
		return i, false, failure("experiment_storage_unavailable")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&i) != nil || d.Decode(new(any)) != io.EOF || i.ExperimentID != id || i.Version != 1 || i.Profile.Validate() != nil || len(i.CommandDigest) != 64 || len(i.Binding) != 64 || len(i.Fixture) > 128 {
		return i, false, failure("experiment_storage_corrupt")
	}
	storedBinding := i.Binding
	i.Binding = ""
	canonical, _ := json.Marshal(i)
	i.Binding = storedBinding
	if digest(canonical) != storedBinding {
		return i, false, failure("experiment_storage_corrupt")
	}
	return i, true, nil
}
func (m *Manager) createIntent(i intent) error {
	return m.createRecord(i, ".json")
}
func (m *Manager) createRecord(i intent, suffix string) error {
	file, e := m.recordPath(i.ExperimentID, suffix)
	if e != nil {
		return e
	}
	dir := filepath.Dir(file)
	if os.MkdirAll(dir, 0700) != nil || privatePath(dir, true) != nil {
		return failure("experiment_storage_unavailable")
	}
	if _, e = m.recordPath(i.ExperimentID, suffix); e != nil {
		return e
	}
	f, e := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return failure("experiment_storage_unavailable")
	}
	defer f.Close()
	if privatePath(file, false) != nil {
		return failure("experiment_storage_unavailable")
	}
	raw, e := json.Marshal(i)
	if e != nil {
		return failure("experiment_storage_unavailable")
	}
	if n, e := f.Write(raw); e != nil || n != len(raw) {
		return failure("experiment_storage_unavailable")
	}
	if f.Sync() != nil || syncDirectory(dir) != nil || syncDirectory(m.dir) != nil {
		return failure("experiment_storage_unavailable")
	}
	return nil
}
