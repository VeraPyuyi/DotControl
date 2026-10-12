package activity

import (
	"github.com/dotcontrol/dotcontrol/internal/model"
	"testing"
	"time"
)

func TestActivityDiagnosticSeparatesPermissionHelperAndSampleFailures(t *testing.T) {
	grant := true
	deny := false
	cases := []struct {
		reason     string
		permission *bool
		available  bool
	}{
		{"permission_missing", &deny, false}, {"invalid_sample", &grant, false},
		{"no_foreground_app", &grant, false}, {"ready", &grant, true},
		{"private-secret-and-title", nil, false},
	}
	for _, tc := range cases {
		m := &Monitor{}
		m.updateSample(model.Activity{Available: tc.available}, tc.reason, tc.permission)
		want := tc.reason
		if tc.permission == nil {
			want = "sample_unavailable"
		}
		if m.Diagnostic().Reason != want || m.Snapshot().Available != tc.available {
			t.Fatal("wrong sample diagnosis")
		}
		m.mu.Lock()
		m.sampled = time.Now().Add(-2 * time.Second)
		m.mu.Unlock()
		if m.Diagnostic().Reason != "sample_stale" || m.Snapshot().Available {
			t.Fatal("stale sample trusted")
		}
		m.markHelperFailed()
		if m.Diagnostic().Reason != "helper_unavailable" || m.Snapshot().Available {
			t.Fatal("exited helper trusted")
		}
	}
}
