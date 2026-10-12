// Package activity tracks activity timestamps, never key contents.
package activity

import (
	"github.com/dotcontrol/dotcontrol/internal/model"
	"sync"
	"time"
)

type Monitor struct {
	mu                                 sync.Mutex
	state                              model.Activity
	sampled                            time.Time
	close                              func()
	reason                             string
	helperFailed                       bool
	permissionKnown, permissionGranted bool
}

type Diagnostic struct {
	Reason                 string `json:"reason"`
	InputMonitoringKnown   bool   `json:"input_monitoring_known"`
	InputMonitoringGranted bool   `json:"input_monitoring_granted"`
}

func (m *Monitor) Diagnostic() Diagnostic {
	m.mu.Lock()
	defer m.mu.Unlock()
	reason := m.reason
	switch {
	case m.helperFailed:
		reason = "helper_unavailable"
	case m.sampled.IsZero():
		reason = "awaiting_sample"
	case time.Since(m.sampled) > time.Second:
		reason = "sample_stale"
	case m.state.Available:
		reason = "ready"
	case reason == "":
		reason = "sample_unavailable"
	}
	return Diagnostic{reason, m.permissionKnown, m.permissionGranted}
}

func (m *Monitor) markHelperFailed() { m.mu.Lock(); m.helperFailed = true; m.mu.Unlock() }

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
	m.updateSample(s, "", nil)
}
func (m *Monitor) updateSample(s model.Activity, reason string, permission *bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = s
	m.sampled = time.Now()
	switch reason {
	case "permission_missing", "invalid_sample", "no_foreground_app", "ready":
		m.reason = reason
	default:
		m.reason = "sample_unavailable"
	}
	m.permissionKnown = permission != nil
	if permission != nil {
		m.permissionGranted = *permission
	}
}
func (m *Monitor) Close() {
	if m.close != nil {
		m.close()
	}
}
