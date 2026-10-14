package desktop

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

// SuperviseRuntime is the foreground Task Scheduler action on Windows. The
// actual agent is an awaited child, never detached. Scheduler's restart policy
// does not reliably retry nonzero process exits; this is the sole retry layer.
// A clean child exit (quit, upgrade, duplicate) ends this action successfully.
func SuperviseRuntime(ctx context.Context, exe, state string) error {
	if e := OwnChildren(); e != nil {
		return e
	}
	return superviseChild(ctx, state, func() error {
		cmd := exec.Command(exe, "--state", state, "serve", "--managed-start", "task-scheduler")
		HideConsole(cmd)
		return cmd.Run()
	}, time.Minute)
}
func superviseChild(ctx context.Context, state string, run func() error, delay time.Duration) error {
	lock := flock.New(filepath.Join(state, "background.lock"))
	defer lock.Close()
	ok, e := lock.TryLock()
	if e != nil {
		return errors.New("background_lock_unavailable")
	}
	if !ok {
		return nil
	}
	for attempt := 0; attempt <= 3; attempt++ {
		blocked, e := RuntimeSuspended(state)
		if e != nil {
			return e
		}
		if blocked || ctx.Err() != nil {
			return nil
		}
		if e = run(); e == nil {
			return nil
		}
		if attempt == 3 {
			return errors.New("background_restart_limit")
		}
		if e = waitBackgroundRetry(ctx, state, delay); e != nil {
			return e
		}
	}
	return nil
}
func waitBackgroundRetry(ctx context.Context, state string, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	poll := time.NewTicker(100 * time.Millisecond)
	defer poll.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			return nil
		case <-poll.C:
			blocked, e := RuntimeSuspended(state)
			if e != nil {
				return e
			}
			if blocked {
				return nil
			}
		}
	}
}
