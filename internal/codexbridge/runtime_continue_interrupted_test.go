package codexbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/connections"
	"github.com/dotcontrol/dotcontrol/internal/engine"
	"github.com/dotcontrol/dotcontrol/internal/experiment"
	"github.com/dotcontrol/dotcontrol/internal/model"
)

func interruptOwnedRuntime(t *testing.T, b *Bridge, p *runtimePeer, r model.CodexSubmit) model.CodexJob {
	t.Helper()
	j, err := b.Submit(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID == "turn-1" })
	if _, err = b.Cancel(r.DeviceID, j.JobID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(4 * time.Second)
	for p.count("turn/interrupt") != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if p.count("turn/interrupt") != 1 {
		t.Fatal("interrupt was not dispatched")
	}
	p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "interrupted"}})
	return waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.State == "cancelled" })
}

func TestContinueInterruptedTerminalPreservesOwnedTaskAndRetry(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(map[bool]string{false: "live", true: "cold_start"}[restart], func(t *testing.T) {
			p := &runtimePeer{}
			cfg, r := runtimeFixture(t, p)
			_, err := connections.NewStore(cfg.Runtime.ConnectionsDir).UpsertServer(connections.ServerProfile{ID: "server-a", Name: "Fixture", SSHAlias: "fixture.invalid", RemoteDir: "/tmp"})
			if err != nil {
				t.Fatal(err)
			}
			r.ServerProfileID, r.ExperimentID = "server-a", "continue-fixture"
			cfg.Runtime.ExperimentStatus = func(_ context.Context, profile, id, binding string) (experiment.Status, error) {
				if profile != r.ServerProfileID || id != r.ExperimentID || len(binding) != 64 {
					t.Error("experiment readback changed binding")
				}
				return experiment.Status{ExperimentID: id, State: "absent"}, nil
			}
			b, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if b != nil {
					b.Close()
				}
			}()
			old := interruptOwnedRuntime(t, b, p, r)
			waitRuntimeInactive(t, b, old.JobID)
			before, err := os.ReadFile(filepath.Join(cfg.StateDir, "codex-runtime-v1.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			binding := b.runtime.jobs[old.JobID].entry.ExperimentBinding
			if len(binding) != 64 {
				t.Fatal("missing frozen experiment binding")
			}
			if restart {
				if err = b.Close(); err != nil {
					t.Fatal(err)
				}
				b, err = New(cfg)
				if err != nil {
					t.Fatal(err)
				}
			}
			next := model.CodexContinue{DeviceID: r.DeviceID, JobID: old.JobID, RequestID: "explicit-continue", Prompt: "Continue the same experiment; request any necessary approval again."}
			queued, err := b.Continue(context.Background(), next)
			if err != nil {
				t.Fatalf("confirmed interrupted terminal task was not continuable: %v", err)
			}
			if queued.State != "queued" || queued.CancelRequested || queued.TurnID != "" || queued.ThreadID != old.ThreadID || queued.ExperimentID != old.ExperimentID || queued.RequestID != old.RequestID || queued.RequestHash != old.RequestHash {
				t.Fatalf("continuation changed ownership or retained old cancellation: %+v", queued)
			}
			j := waitRuntime(t, b, old.JobID, func(j model.CodexJob) bool { return j.TurnID == "turn-2" })
			if _, err = b.Continue(context.Background(), next); err != nil {
				t.Fatal(err)
			}
			changed := next
			changed.Prompt = "changed request"
			if _, err = b.Continue(context.Background(), changed); err == nil || err.Error() != "request_id_conflict" {
				t.Fatalf("changed retry accepted: %v", err)
			}
			p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "completed"}})
			waitRuntimeInactive(t, b, old.JobID)
			if p.count("thread/start") != 1 || p.count("thread/resume") != 1 || p.count("turn/start") != 2 || p.count("turn/interrupt") != 1 {
				t.Fatal("continuation created another thread, replayed, or inherited cancellation")
			}
			if got := b.runtime.jobs[old.JobID].entry.ExperimentBinding; got != binding {
				t.Fatal("frozen experiment binding changed")
			}
			if err = b.Close(); err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(filepath.Join(cfg.StateDir, "codex-runtime-v1.jsonl"))
			if err != nil || !bytes.HasPrefix(after, before) || bytes.Contains(after, []byte(next.Prompt)) {
				t.Fatal("old journal bytes changed or private prompt was persisted", err)
			}
			b, err = New(cfg)
			if err != nil {
				t.Fatalf("valid explicit cancellation reset did not replay: %v", err)
			}
			if _, err = b.Continue(context.Background(), next); err != nil || p.count("turn/start") != 2 {
				t.Fatal("cold retry replayed continuation", err)
			}
		})
	}
}

func TestContinueInterruptedWaitsForConnectionCleanup(t *testing.T) {
	p := &runtimePeer{}
	cfg, r := runtimeFixture(t, p)
	start := cfg.Runtime.StartClient
	closing, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	first := true
	cfg.Runtime.StartClient = func(ctx context.Context, exe, cwd string) (*Client, error) {
		c, err := start(ctx, exe, cwd)
		if err == nil && first {
			first = false
			c.stop = func() error { close(closing); <-release; return nil }
		}
		return c, err
	}
	b, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	defer unblock()
	j := interruptOwnedRuntime(t, b, p, r)
	select {
	case <-closing:
	case <-time.After(4 * time.Second):
		t.Fatal("connection cleanup did not start")
	}
	next := model.CodexContinue{DeviceID: r.DeviceID, JobID: j.JobID, RequestID: "after-cleanup", Prompt: "Continue"}
	if _, err = b.Continue(context.Background(), next); err == nil || err.Error() != "job_not_continuable" {
		t.Fatalf("continued while original client was closing: %v", err)
	}
	unblock()
	waitRuntimeInactive(t, b, j.JobID)
	if _, err = b.Continue(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID == "turn-2" })
	p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "completed"}})
	waitRuntimeInactive(t, b, j.JobID)
}

func TestContinueInterruptedRejectsInsufficientStopEvidence(t *testing.T) {
	tests := map[string]func(*runtimeJob){
		"unknown":              func(j *runtimeJob) { j.entry.Job.State = "outcome_unknown" },
		"blocked":              func(j *runtimeJob) { j.entry.Job.State = "blocked" },
		"unconfirmed_stop":     func(j *runtimeJob) { j.entry.Job.Code = "interrupt_stop_unconfirmed" },
		"before_dispatch":      func(j *runtimeJob) { j.entry.Job.Code = "cancelled_before_execution" },
		"wrong_turn_state":     func(j *runtimeJob) { j.entry.Job.Runtime.TurnState = "completed" },
		"missing_runtime":      func(j *runtimeJob) { j.entry.Job.Runtime = nil },
		"not_terminal_receipt": func(j *runtimeJob) { j.entry.Phase = "turn_bound" },
		"missing_thread":       func(j *runtimeJob) { j.entry.Job.ThreadID = "" },
		"missing_turn":         func(j *runtimeJob) { j.entry.Job.TurnID = "" },
		"native":               func(j *runtimeJob) { j.entry.Job.Runtime.Execution = "computer_use" },
		"active":               func(j *runtimeJob) { j.active = true },
		"client":               func(j *runtimeJob) { j.client = &Client{} },
		"lease":                func(j *runtimeJob) { j.lease = &engine.DelegationLease{} },
		"desktop_pending":      func(j *runtimeJob) { j.entry.DesktopPending = true },
		"cancelled_completed":  func(j *runtimeJob) { j.entry.Job.State = "completed" },
		"cancelled_failed":     func(j *runtimeJob) { j.entry.Job.State = "failed" },
		"cancelled_takeover":   func(j *runtimeJob) { j.entry.Job.State = "local_takeover" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			p := &runtimePeer{}
			cfg, r := runtimeFixture(t, p)
			b, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			j := interruptOwnedRuntime(t, b, p, r)
			waitRuntimeInactive(t, b, j.JobID)
			b.mu.Lock()
			owned := b.runtime.jobs[j.JobID]
			original := *owned
			owned.entry.Job = cloneJob(owned.entry.Job)
			mutate(owned)
			b.mu.Unlock()
			_, err = b.Continue(context.Background(), model.CodexContinue{DeviceID: r.DeviceID, JobID: j.JobID, RequestID: "must-reject", Prompt: "Continue"})
			b.mu.Lock()
			*owned = original
			b.mu.Unlock()
			if err == nil || err.Error() != "job_not_continuable" || p.count("thread/resume") != 0 {
				t.Fatalf("insufficient evidence accepted: %v", err)
			}
		})
	}
}

func TestContinueInterruptedSaveFailurePreservesCancellation(t *testing.T) {
	p := &runtimePeer{}
	cfg, r := runtimeFixture(t, p)
	b, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if b != nil {
			b.Close()
		}
	}()
	j := interruptOwnedRuntime(t, b, p, r)
	waitRuntimeInactive(t, b, j.JobID)
	b.mu.Lock()
	before, _ := json.Marshal(b.runtime.jobs[j.JobID].entry)
	err = b.runtime.file.Close()
	b.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	next := model.CodexContinue{DeviceID: r.DeviceID, JobID: j.JobID, RequestID: "failed-intent", Prompt: "Continue"}
	if _, err = b.Continue(context.Background(), next); err == nil || err.Error() != "storage_unavailable" {
		t.Fatalf("expected durable intent failure, got %v", err)
	}
	b.mu.Lock()
	after, _ := json.Marshal(b.runtime.jobs[j.JobID].entry)
	_, registered := b.runtime.requests[next.RequestID]
	b.mu.Unlock()
	if !bytes.Equal(before, after) || registered || p.count("thread/resume") != 0 {
		t.Fatal("failed save changed live cancellation, request dedupe, or dispatched")
	}
	if err = b.Close(); err != nil {
		t.Fatal(err)
	}
	b, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.Continue(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID == "turn-2" })
	p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "completed"}})
	waitRuntimeInactive(t, b, j.JobID)
}

func TestRuntimeReplayRejectsInvalidCancellationReset(t *testing.T) {
	tests := map[string]func(*runtimeEntry){
		"no_continue_phase":     func(e *runtimeEntry) { e.Phase = "turn_terminal" },
		"old_request":           func(e *runtimeEntry) { e.CurrentRequestID = e.Job.RequestID; delete(e.Requests, "forged-continue") },
		"no_new_request_hash":   func(e *runtimeEntry) { delete(e.Requests, "forged-continue") },
		"multiple_new_requests": func(e *runtimeEntry) { e.Requests["second-forged"] = strings.Repeat("b", 64) },
		"retained_turn":         func(e *runtimeEntry) { e.Job.TurnID = "turn-1" },
		"wrong_thread":          func(e *runtimeEntry) { e.Job.ThreadID = "foreign-thread" },
		"wrong_state":           func(e *runtimeEntry) { e.Job.State = "completed" },
		"wrong_code":            func(e *runtimeEntry) { e.Job.Code = "model_turn_completed" },
		"retained_turn_state":   func(e *runtimeEntry) { e.Job.Runtime.TurnState = "interrupted" },
		"desktop_pending":       func(e *runtimeEntry) { e.DesktopPending = true },
		"native":                func(e *runtimeEntry) { e.Job.Runtime.Execution = "computer_use" },
		"changed_experiment":    func(e *runtimeEntry) { e.Job.ExperimentID = "different-experiment" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			p := &runtimePeer{}
			cfg, r := runtimeFixture(t, p)
			b, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			j := interruptOwnedRuntime(t, b, p, r)
			waitRuntimeInactive(t, b, j.JobID)
			b.mu.Lock()
			e := b.runtime.jobs[j.JobID].entry
			e.Job = cloneJob(e.Job)
			e.Requests = map[string]string{}
			for id, hash := range b.runtime.jobs[j.JobID].entry.Requests {
				e.Requests[id] = hash
			}
			e.Requests["forged-continue"] = strings.Repeat("a", 64)
			e.CurrentRequestID, e.Phase = "forged-continue", "continue_intent"
			e.Job.CancelRequested = false
			e.Job.State, e.Job.Code, e.Job.TurnID, e.Job.Runtime.TurnState = "queued", "request_persisted", "", ""
			mutate(&e)
			err = b.saveRuntimeLocked(&runtimeJob{entry: e})
			b.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if err = b.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := New(cfg)
			if err == nil {
				reopened.Close()
				t.Fatal("authenticated non-continuation cancellation reset accepted")
			}
			if err.Error() != "storage_corrupt" {
				t.Fatal(err)
			}
		})
	}
}

func TestInterruptedContinueCrashAtIntentNeverReplaysOrUsesOldTurn(t *testing.T) {
	p := &runtimePeer{}
	cfg, r := runtimeFixture(t, p)
	b, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	j := interruptOwnedRuntime(t, b, p, r)
	waitRuntimeInactive(t, b, j.JobID)
	next := model.CodexContinue{DeviceID: r.DeviceID, JobID: j.JobID, RequestID: "crash-intent", Prompt: "Continue"}
	if _, err = b.Continue(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID == "turn-2" })
	p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "completed"}})
	waitRuntimeInactive(t, b, j.JobID)
	if err = b.Close(); err != nil {
		t.Fatal(err)
	}
	ledger, err := os.ReadFile(filepath.Join(cfg.StateDir, "codex-runtime-v1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var prefix []byte
	found := false
	for _, line := range bytes.SplitAfter(ledger, []byte("\n")) {
		prefix = append(prefix, line...)
		var record runtimeRecord
		if json.Unmarshal(line, &record) == nil && record.Entry.Phase == "continue_intent" && record.Entry.CurrentRequestID == next.RequestID {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("missing durable continuation intent")
	}
	crashDir := t.TempDir()
	for _, name := range []string{"codex-jobs.key", "codex-jobs.jsonl"} {
		data, err := os.ReadFile(filepath.Join(cfg.StateDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(crashDir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.WriteFile(filepath.Join(crashDir, "codex-runtime-v1.jsonl"), prefix, 0600); err != nil {
		t.Fatal(err)
	}
	cfg.StateDir = crashDir
	recovered, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	waitRuntimeInactive(t, recovered, j.JobID)
	j, err = recovered.Continue(context.Background(), next)
	if err != nil || j.State != "outcome_unknown" || j.Code != "turn_binding_unknown" || j.TurnID != "" {
		t.Fatalf("old turn resolved an unbound continuation: %+v %v", j, err)
	}
	if p.count("turn/start") != 2 || p.count("thread/resume") != 1 || p.count("thread/read") != 1 {
		t.Fatal("crash recovery or retry dispatched continuation")
	}
}

func TestContinueInterruptedRequiresDurableStopReceipt(t *testing.T) {
	p := &runtimePeer{}
	cfg, r := runtimeFixture(t, p)
	b, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	j, err := b.Submit(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID == "turn-1" })
	// Fail the terminal receipt append. The event may update memory but cannot
	// authorize another turn without its durable official stop evidence.
	b.mu.Lock()
	err = b.runtime.file.Close()
	b.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "interrupted"}})
	waitRuntimeInactive(t, b, j.JobID)
	_, err = b.Continue(context.Background(), model.CodexContinue{DeviceID: r.DeviceID, JobID: j.JobID, RequestID: "undurable-stop", Prompt: "Continue"})
	if err == nil || err.Error() != "storage_unavailable" || p.count("thread/resume") != 0 {
		t.Fatalf("undurable stop receipt allowed continuation: %v", err)
	}
}
