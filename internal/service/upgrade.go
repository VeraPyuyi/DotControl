package service

import (
	"context"
	"errors"
	"sync"
	"time"
)

// UpgradeGate stops new work without cancelling existing work or changing the
// persistent pause choice. Both MCP and gateway RPC must share this instance.
type UpgradeGate struct {
	Backend  Backend
	Ready    func() error
	mu       sync.Mutex
	draining bool
	active   int
}

func (g *UpgradeGate) Draining() bool { g.mu.Lock(); defer g.mu.Unlock(); return g.draining }
func (g *UpgradeGate) Call(ctx context.Context, method string, r Request) (any, error) {
	if method != "execute" && method != "codex_submit" && method != "codex_continue" {
		return g.Backend.Call(ctx, method, r)
	}
	g.mu.Lock()
	if g.draining {
		g.mu.Unlock()
		return nil, errors.New("upgrade_in_progress")
	}
	g.active++
	g.mu.Unlock()
	defer func() { g.mu.Lock(); g.active--; g.mu.Unlock() }()
	return g.Backend.Call(ctx, method, r)
}

// Drain is called only by the authenticated local shutdown-for-upgrade route.
// Success keeps admission closed until process exit; failure restores it.
func (g *UpgradeGate) Drain(ctx context.Context) (err error) {
	g.mu.Lock()
	if g.draining {
		g.mu.Unlock()
		return errors.New("upgrade_in_progress")
	}
	g.draining = true
	g.mu.Unlock()
	defer func() {
		if err != nil {
			g.mu.Lock()
			g.draining = false
			g.mu.Unlock()
		}
	}()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		g.mu.Lock()
		active := g.active
		g.mu.Unlock()
		if active == 0 {
			if g.Ready == nil {
				return errors.New("upgrade_state_unavailable")
			}
			if err = g.Ready(); err == nil {
				return ctx.Err()
			}
			if err.Error() != "upgrade_busy" {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
