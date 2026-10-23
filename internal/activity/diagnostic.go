package activity

import (
	"github.com/dotcontrol/dotcontrol/internal/model"
	"time"
)

type Diagnostic struct {
	Reason                 string `json:"reason"`
	InputMonitoringKnown   bool   `json:"input_monitoring_known"`
	InputMonitoringGranted bool   `json:"input_monitoring_granted"`
}

func (m *Monitor) Diagnostic() Diagnostic {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.diagnostic
	switch {
	case m.helperFailed:
		d.Reason = "helper_unavailable"
	case m.sampled.IsZero():
		d.Reason = "awaiting_sample"
	case time.Since(m.sampled) > time.Second:
		d.Reason = "sample_stale"
	case m.state.Available:
		d.Reason = "ready"
	case d.Reason == "":
		d.Reason = "sample_unavailable"
	}
	return d
}
func (m *Monitor) updateSample(s model.Activity, reason string, permission *bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = s
	m.sampled = time.Now()
	d := Diagnostic{Reason: "sample_unavailable", InputMonitoringKnown: permission != nil}
	switch reason {
	case "permission_missing", "invalid_sample", "no_foreground_app", "ready":
		d.Reason = reason
	}
	if permission != nil {
		d.InputMonitoringGranted = *permission
	}
	m.diagnostic = d
}
func (m *Monitor) markHelperFailed() { m.mu.Lock(); m.helperFailed = true; m.mu.Unlock() }
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
