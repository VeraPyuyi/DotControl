package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/dotcontrol/dotcontrol/internal/config"
	"github.com/dotcontrol/dotcontrol/internal/desktop"
	"github.com/dotcontrol/dotcontrol/internal/install"
	"github.com/gofrs/flock"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

func installationLock(ctx context.Context, path string) (func(), error) {
	// Normalize only the filesystem lock; rewriting the runtime --state value
	// would change the existing operating-system credential namespace.
	canonical, e := install.CanonicalPath(path)
	if e != nil {
		return nil, e
	}
	path = canonical
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		info, e := os.Lstat(p)
		if e != nil && !os.IsNotExist(e) {
			return nil, e
		}
		if e == nil && (info.Mode()&os.ModeSymlink != 0 || (p == filepath.Clean(path) && !info.Mode().IsRegular())) {
			return nil, errors.New("installation_lock_unsafe")
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return nil, e
	}
	lock := flock.New(path)
	wait, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ok, e := lock.TryLockContext(wait, 50*time.Millisecond)
	if e != nil || !ok {
		lock.Close()
		return nil, errors.New("installation_in_progress")
	}
	return func() { _ = lock.Close() }, nil
}

// The actual filesystem object serializes aliases and distinct program roots.
func lockInstallationState(ctx context.Context, state string) (func(), error) {
	return installationLock(ctx, filepath.Join(state, "installer.lock"))
}

// An occupied state is rejected before any coordination file is created. The
// later held background and engine locks close runtime-start/backoff races.
func checkNoStartBusy(state string) error {
	for _, name := range []string{"background.lock", "engine.lock"} {
		if e := checkNoStartLock(filepath.Join(state, name)); e != nil {
			return e
		}
	}
	return nil
}
func checkNoStartLock(p string) error {
	info, e := os.Lstat(p)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if !info.Mode().IsRegular() {
		return errors.New("installation_lock_unsafe")
	}
	lock := flock.New(p)
	defer lock.Close()
	ok, e := lock.TryLock()
	if e != nil {
		return e
	}
	if !ok {
		return errors.New("no_start_state_busy")
	}
	return nil
}
func lockNoStartRuntime(state string) (func(), error) {
	var locks []*flock.Flock
	release := func() {
		for i := len(locks) - 1; i >= 0; i-- {
			_ = locks[i].Close()
		}
	}
	for _, name := range []string{"background.lock", "engine.lock"} {
		lock := flock.New(filepath.Join(state, name))
		locks = append(locks, lock)
		ok, e := lock.TryLock()
		if e != nil {
			release()
			return nil, e
		}
		if !ok {
			release()
			return nil, errors.New("no_start_state_busy")
		}
	}
	return release, nil
}
func lockInstallationSettings(ctx context.Context) (func(), error) {
	base, e := os.UserConfigDir()
	if e != nil {
		return nil, e
	}
	return installationLock(ctx, filepath.Join(base, "DotControl-install", "settings.lock"))
}
func startupBelongsToState(s desktop.StartupSnapshot, state string) bool {
	return s.BelongsToState(state)
}
func snapshotProgramPaths(s *activationSnapshot) []string {
	paths := []string{}
	if s.Previous != nil {
		paths = append(paths, s.Previous.Executable)
	}
	if s.MissingPrevious != "" {
		paths = append(paths, s.MissingPrevious)
	}
	if runtime.GOOS == "windows" && len(s.Config) > 0 {
		var c config.Config
		if json.Unmarshal(s.Config, &c) == nil {
			paths = append(paths, c.DriverPath, c.TunnelPath)
		}
	}
	return paths
}
