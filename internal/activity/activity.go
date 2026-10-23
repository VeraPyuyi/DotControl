// Package activity tracks activity timestamps, never key contents.
package activity

import (
	"github.com/dotcontrol/dotcontrol/internal/model"
	"sync"
	"time"
)

type Monitor struct {
	mu           sync.Mutex
	state        model.Activity
	sampled      time.Time
	close        func()
	diagnostic   Diagnostic
	helperFailed bool
}

func (m *Monitor) Snapshot() model.Activity {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.state
	// A stopped/crashed helper is never evidence that the user is idle.
	if m.helperFailed || m.sampled.IsZero() || time.Since(m.sampled) > time.Second {
		s.Available = false
	}
	return s
}
func (m *Monitor) update(s model.Activity) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = s
	m.sampled = time.Now()
}
func (m *Monitor) Close() {
	if m.close != nil {
		m.close()
	}
}
