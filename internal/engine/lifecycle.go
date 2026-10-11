package engine

import (
	"context"
	"io"

	"github.com/dotcontrol/dotcontrol/internal/model"
)

// Reconnect holds the same sole-writer gate as desktop actions. No call may
// use an observation from the old connection, even if its coordinates match.
// The factory must always return a non-nil fail-closed driver on failure.
func (e *Engine) Reconnect(ctx context.Context, factory func() model.Driver) error {
	if err := e.acquire(ctx); err != nil {
		return err
	}
	defer e.unlock()
	e.mu.Lock()
	e.observationGeneration++
	err := e.healthyLocked()
	e.observations = map[string]model.Observation{}
	e.latest = map[model.Target]string{}
	e.mu.Unlock()
	if err != nil {
		return err
	}
	if c, ok := e.driver.(io.Closer); ok {
		_ = c.Close()
	}
	next := factory()
	if next == nil {
		return fail("driver_unavailable")
	}
	e.driver = next
	return nil
}

// Invalidate is used when remote access changes; old coordinate leases must
// never survive a new connection or permission grant.
func (e *Engine) Invalidate() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.observationGeneration++
	e.observations = map[string]model.Observation{}
	e.latest = map[model.Target]string{}
}

func (e *Engine) Rename(name string) { e.mu.Lock(); defer e.mu.Unlock(); e.cfg.Name = name }
