package activity

import (
	"context"
	"github.com/dotcontrol/dotcontrol/internal/model"
	"sync"
)

// Managed can replace a failed helper while its identity remains stable. A new
// generation invalidates foreground authority even if old/new counters match.
type Managed struct {
	mu      sync.Mutex
	ctx     context.Context
	helper  string
	current *Monitor
	epoch   uint64
}

func NewManaged(ctx context.Context, helper string) *Managed {
	m := &Managed{ctx: ctx, helper: helper}
	m.Restart()
	return m
}
func (m *Managed) Restart() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current != nil {
		m.current.Close()
	}
	m.epoch++
	m.current, _ = Start(m.ctx, m.helper)
}
func (m *Managed) Snapshot() model.Activity {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := model.Activity{Available: false}
	if m.current != nil {
		s = m.current.Snapshot()
	}
	s.Generation += m.epoch << 48
	return s
}
func (m *Managed) Diagnostic() Diagnostic {
	if m == nil {
		return Diagnostic{Reason: "helper_unavailable"}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == nil {
		return Diagnostic{Reason: "helper_unavailable"}
	}
	return m.current.Diagnostic()
}
func (m *Managed) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current != nil {
		m.current.Close()
		m.current = nil
	}
}
