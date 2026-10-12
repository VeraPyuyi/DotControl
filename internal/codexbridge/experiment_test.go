package codexbridge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dotcontrol/dotcontrol/internal/connections"
	"github.com/dotcontrol/dotcontrol/internal/experiment"
	"github.com/dotcontrol/dotcontrol/internal/model"
)

func TestExperimentIDRequiresSavedServerAndPreservesRetryHash(t *testing.T) {
	peer := &runtimePeer{}
	cfg, r := runtimeFixture(t, peer)
	r.ExperimentID = "fixture-a"
	b, e := New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	j, e := b.Submit(context.Background(), r)
	if e != nil || j.State != "rejected" || j.Code != "invalid_experiment_request" {
		t.Fatalf("%+v %v", j, e)
	}
	again, e := b.Submit(context.Background(), r)
	if e != nil || again.JobID != j.JobID || again.RequestHash != j.RequestHash {
		t.Fatalf("%+v %v", again, e)
	}
	r.ExperimentID = "fixture-b"
	if _, e = b.Submit(context.Background(), r); e == nil || e.Error() != "request_id_conflict" {
		t.Fatal(e)
	}
}
func TestExperimentInstructionsUseOnlyHelperReferences(t *testing.T) {
	text := experimentInstructions("/private/state", "server-a", "fixture-a")
	for _, want := range []string{"exec_command", "--state", "--profile", "server-a", "--experiment-id", "fixture-a", "status", "submit", "does not stop", "Never run direct SSH"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q: %s", want, text)
		}
	}
}
func TestExperimentEvidenceComesFromHelperNotModelAndCancelNeverStopsIt(t *testing.T) {
	cfg, r := runtimeFixture(t, &runtimePeer{})
	// Keep execution blocked; install an already-started owned record below to
	// exercise readback without asking a model or launching a remote process.
	cfg.Runtime.Acceptance = nil
	_, e := connections.NewStore(cfg.Runtime.ConnectionsDir).UpsertServer(connections.ServerProfile{ID: "server-a", Name: "Fixture", SSHAlias: "example.invalid", RemoteDir: "/tmp"})
	if e != nil {
		t.Fatal(e)
	}
	calls := 0
	cfg.Runtime.ExperimentStatus = func(_ context.Context, profile, id string) (experiment.Status, error) {
		calls++
		if profile != "server-a" || id != "fixture-a" {
			t.Fatal(profile, id)
		}
		zero := 0
		return experiment.Status{ExperimentID: id, State: "completed", Fixture: experiment.Fixture, Expected: true, Result: experiment.ExpectedResult, ResultSHA256: strings.Repeat("a", 64), ExitCode: &zero}, nil
	}
	r.ServerProfileID = "server-a"
	r.ExperimentID = "fixture-a"
	b, e := New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	j, e := b.Submit(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	b.mu.Lock()
	owned := b.runtime.jobs[j.JobID]
	owned.entry.Job.State = "completed"
	owned.entry.Job.Runtime.Text = "MODEL SAYS SUCCESS"
	b.mu.Unlock()
	j, e = b.Job(r.DeviceID, j.JobID)
	if e != nil || j.Runtime.Experiment == nil || j.Runtime.Experiment.State != "completed" || j.Runtime.Verification != "verified_fixture" || j.Runtime.Experiment.Result != experiment.ExpectedResult || calls != 1 {
		t.Fatalf("%+v %v calls %d", j, e, calls)
	}
	_, e = b.Cancel(r.DeviceID, j.JobID)
	if e != nil || calls != 1 {
		t.Fatalf("cancel touched experiment: %v %d", e, calls)
	}
	// Ephemeral readback evidence must not alter the authenticated history.
	if journalEntry(owned.entry).Job.Runtime.Experiment != nil {
		t.Fatal("experiment evidence persisted in general journal")
	}
}
func TestAbsentExperimentFieldDoesNotChangeLegacySerialization(t *testing.T) {
	data, _ := json.Marshal(model.CodexSubmit{DeviceID: "d", RequestID: "r", Prompt: "p"})
	if strings.Contains(string(data), "experiment") {
		t.Fatal(string(data))
	}
	want := `{"device_id":"d","request_id":"r","prompt":"p","allowed_apps":null,"working_dirs":null}`
	if string(data) != want {
		t.Fatal(string(data))
	}
}
