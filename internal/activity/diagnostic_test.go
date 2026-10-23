package activity

import (
	"github.com/dotcontrol/dotcontrol/internal/model"
	"testing"
	"time"
)

func TestActivityFailureEvidence(t *testing.T) {
	m := &Monitor{}
	if m.Diagnostic().Reason != "awaiting_sample" {
		t.Fatal(m.Diagnostic())
	}
	for _, tc := range []struct {
		reason     string
		permission *bool
		available  bool
		want       string
	}{
		{"permission_missing", boolValue(false), false, "permission_missing"},
		{"invalid_sample", boolValue(true), false, "invalid_sample"},
		{"no_foreground_app", boolValue(true), false, "no_foreground_app"},
		{"private title", nil, false, "sample_unavailable"},
		{"ready", boolValue(true), true, "ready"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			m.updateSample(model.Activity{Available: tc.available}, tc.reason, tc.permission)
			d := m.Diagnostic()
			if d.Reason != tc.want || d.InputMonitoringKnown != (tc.permission != nil) {
				t.Fatalf("%+v", d)
			}
		})
	}
	m.sampled = time.Now().Add(-2 * time.Second)
	if m.Diagnostic().Reason != "sample_stale" || m.Snapshot().Available {
		t.Fatal("stale sample trusted")
	}
	m.update(model.Activity{Available: true})
	m.markHelperFailed()
	if m.Diagnostic().Reason != "helper_unavailable" || m.Snapshot().Available {
		t.Fatal("exited helper trusted")
	}
}
func boolValue(v bool) *bool { return &v }
func TestInactiveActivityIsNotPermissionDenial(t *testing.T) {
	m := NewInactive(nil, "")
	if m.Diagnostic().Reason != "helper_unavailable" || m.Diagnostic().InputMonitoringKnown {
		t.Fatal(m.Diagnostic())
	}
}
