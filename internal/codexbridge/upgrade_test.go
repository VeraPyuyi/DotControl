package codexbridge

import (
	"github.com/dotcontrol/dotcontrol/internal/model"
	"testing"
)

func TestUpgradeReadinessWaitsCleanupAndRejectsUncertain(t *testing.T) {
	b := &Bridge{runtime: &runtimeState{jobs: map[string]*runtimeJob{}}}
	for _, tc := range []struct {
		state           string
		active, desktop bool
		want            string
	}{
		{"completed", false, false, ""}, {"blocked", false, false, ""},
		{"running", true, false, "upgrade_busy"}, {"completed", true, false, "upgrade_busy"},
		{"outcome_unknown", false, false, "upgrade_unresolved"},
		{"cancelled", false, true, "upgrade_unresolved"},
	} {
		j := &runtimeJob{active: tc.active, entry: runtimeEntry{Job: model.CodexJob{State: tc.state}, DesktopPending: tc.desktop}}
		b.runtime.jobs["test"] = j
		err := b.UpgradeReady()
		got := ""
		if err != nil {
			got = err.Error()
		}
		if got != tc.want {
			t.Fatalf("%s: got %q want %q", tc.state, got, tc.want)
		}
		if j.active != tc.active || j.entry.Job.State != tc.state {
			t.Fatal("readiness changed work")
		}
	}
}
