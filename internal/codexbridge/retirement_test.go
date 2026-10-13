package codexbridge

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/model"
)

func TestResolvedUnexpectedRequestStillRequiresInterruption(t *testing.T) {
	c := fakeClient(t, func(conn net.Conn) {
		enc := json.NewEncoder(conn)
		_ = enc.Encode(map[string]any{"id": "unexpected", "method": "item/tool/requestUserInput", "params": map[string]any{}})
		_ = enc.Encode(map[string]any{"method": "serverRequest/resolved", "params": map[string]any{"requestId": "unexpected"}})
		var buffer [1]byte
		_, _ = conn.Read(buffer[:])
	})
	select {
	case <-c.Notifications():
	case <-time.After(time.Second):
		t.Fatal("resolution notification missing")
	}
	if len(c.PendingRequests()) != 1 {
		t.Fatal("resolved unexpected input escaped the interruption guard")
	}
}

func TestRetiredSubmitRejectsBeforeRuntimeContact(t *testing.T) {
	for _, field := range []string{"server", "experiment", "both"} {
		t.Run(field, func(t *testing.T) {
			peer := &runtimePeer{}
			cfg, r := runtimeFixture(t, peer)
			b, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			if field != "experiment" {
				r.ServerProfileID = "old-server"
			}
			if field != "server" {
				r.ExperimentID = "old-experiment"
			}
			_, err = b.Submit(context.Background(), r)
			if err == nil || err.Error() != "feature_retired" {
				t.Fatalf("retired request: %v", err)
			}
			if peer.count("initialize") != 0 || len(b.runtime.jobs) != 0 {
				t.Fatal("retired request started or persisted runtime")
			}
		})
	}
}

func TestStartupPendingRequestStopsWithoutApproval(t *testing.T) {
	for _, method := range []string{"permissionProfile/list", "thread/start", "turn/start"} {
		t.Run(method, func(t *testing.T) {
			peer := &runtimePeer{blockMethod: method, blockEntered: make(chan struct{}), blockRelease: make(chan struct{})}
			cfg, r := runtimeFixture(t, peer)
			b, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			j, err := b.Submit(context.Background(), r)
			if err != nil {
				t.Fatal(err)
			}
			<-peer.blockEntered
			peer.send(map[string]any{"id": "setup-input", "method": "item/tool/requestUserInput", "params": map[string]any{}})
			latched := false
			deadline := time.Now().Add(500 * time.Millisecond)
			for time.Now().Before(deadline) {
				got, _ := b.Job(r.DeviceID, j.JobID)
				if got.Code == "needs_local_setup" && got.CancelRequested {
					latched = true
					break
				}
				time.Sleep(time.Millisecond)
			}
			close(peer.blockRelease)
			if !latched {
				t.Fatal("pending setup request did not stop startup")
			}
			waitRuntimeInactive(t, b, j.JobID)
			got, _ := b.Job(r.DeviceID, j.JobID)
			if got.Code != "needs_local_setup" || peer.count("") != 0 {
				t.Fatalf("setup request answered or lost: %+v", got)
			}
			if method != "turn/start" && peer.count("turn/start") != 0 {
				t.Fatal("turn dispatched after pending setup request")
			}
			if method == "turn/start" && (got.State != "outcome_unknown" || got.TurnID != "" || peer.count("turn/interrupt") != 0) {
				t.Fatalf("unbound request guessed a turn or lost uncertainty: %+v", got)
			}
		})
	}
}

func TestBoundStartupPendingRequestInterruptsOnceWithoutStartResponse(t *testing.T) {
	for _, tc := range []struct {
		name, startResponse string
		receipt             bool
	}{
		{name: "missing_response_unconfirmed"},
		{name: "delayed_response_confirmed", startResponse: "after_interrupt", receipt: true},
		{name: "response_wins_confirmed", startResponse: "before_interrupt", receipt: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			peer := &runtimePeer{deferredTurnResponse: make(chan any, 1)}
			cfg, r := runtimeFixture(t, peer)
			b, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			j, err := b.Submit(context.Background(), r)
			if err != nil {
				t.Fatal(err)
			}
			var startResponse any
			select {
			case startResponse = <-peer.deferredTurnResponse:
			case <-time.After(time.Second):
				t.Fatal("turn start was not dispatched")
			}
			if tc.startResponse == "before_interrupt" {
				peer.send(startResponse)
				// Make the response-winning branch deterministic: do not let the
				// startup watcher cancel the call before its response is bound.
				waitRuntime(t, b, j.JobID, func(got model.CodexJob) bool { return got.TurnID == "turn-1" && got.State == "running" })
			}
			peer.send(map[string]any{"id": "setup-input", "method": "item/tool/requestUserInput", "params": map[string]any{"threadId": "owned-thread", "turnId": "turn-1"}})
			// Wait on the same bounded runtime helper used elsewhere, including
			// journal persistence. This is a protocol assertion, not a 1s SLA.
			waitRuntime(t, b, j.JobID, func(got model.CodexJob) bool {
				return (got.Code == "needs_local_setup" && peer.count("turn/interrupt") != 0) || runtimeTerminal(got.State)
			})
			if peer.count("turn/interrupt") != 1 {
				got, _ := b.Job(r.DeviceID, j.JobID)
				b.mu.Lock()
				active, phase := b.runtime.jobs[j.JobID].active, b.runtime.jobs[j.JobID].entry.Phase
				b.mu.Unlock()
				t.Fatalf("bound pending startup request interrupt count=%d active=%t phase=%s job=%+v", peer.count("turn/interrupt"), active, phase, got)
			}
			peer.mu.Lock()
			var params struct{ ThreadID, TurnID string }
			_ = json.Unmarshal(peer.params["turn/interrupt"][0], &params)
			peer.mu.Unlock()
			if params.ThreadID != "owned-thread" || params.TurnID != "turn-1" {
				t.Fatalf("interrupt was not bound to the owned turn: %+v", params)
			}
			if err = b.ConfigureTerminal(true, false, "", "", ""); err == nil || err.Error() != "terminal_runtime_busy" {
				t.Fatalf("interrupt acknowledgement released guard: %v", err)
			}
			if tc.startResponse == "after_interrupt" {
				peer.send(startResponse)
			}
			if tc.receipt {
				peer.notification("turn/completed", map[string]any{"threadId": "owned-thread", "turn": map[string]any{"id": "turn-1", "status": "interrupted"}})
			}
			waitRuntimeInactive(t, b, j.JobID)
			got, _ := b.Job(r.DeviceID, j.JobID)
			if got.Code != "needs_local_setup" || !got.CancelRequested || peer.count("") != 0 || peer.count("turn/interrupt") != 1 || peer.count("turn/start") != 1 {
				t.Fatalf("startup request answered, replayed, or lost: %+v", got)
			}
			if tc.receipt {
				if got.State != "blocked" || got.Runtime.TurnState != "interrupted" {
					t.Fatalf("stop receipt was not recorded: %+v", got)
				}
			} else {
				if got.State != "outcome_unknown" {
					t.Fatalf("unconfirmed stop was treated as settled: %+v", got)
				}
				if err = b.ConfigureTerminal(true, false, "", "", ""); err == nil || err.Error() != "terminal_runtime_busy" {
					t.Fatalf("unconfirmed stop released guard: %v", err)
				}
			}
		})
	}
}

func TestRetiredStoredRequestReplaysWithoutChangingJournal(t *testing.T) {
	peer := &runtimePeer{}
	cfg, r := runtimeFixture(t, peer)
	terminalPolicy(t, cfg, "never")
	cfg.Runtime.Acceptance = nil
	b, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r.ServerProfileID, r.ExperimentID = "legacy-server", "legacy-experiment"
	raw, _ := json.Marshal(r)
	original, err := b.Submit(context.Background(), model.CodexSubmit{DeviceID: r.DeviceID, RequestID: r.RequestID, ProfileID: r.ProfileID, Prompt: r.Prompt})
	if err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	j := b.runtime.jobs[original.JobID]
	// This fixture models an authenticated alpha.4 terminal experiment record.
	if j.cancel != nil {
		j.cancel()
	}
	b.mu.Unlock()
	waitRuntimeInactive(t, b, original.JobID)
	b.mu.Lock()
	j.entry.Job.State, j.entry.Job.Code = "outcome_unknown", "interrupt_stop_unconfirmed"
	j.entry.Job.ThreadID = "legacy-thread"
	j.entry.Job.ServerProfileID, j.entry.Job.ExperimentID = r.ServerProfileID, r.ExperimentID
	j.entry.Job.RequestHash = b.hash("request", raw)
	j.entry.Requests[r.RequestID] = j.entry.Job.RequestHash
	j.entry.ExperimentBinding = "legacy-binding"
	j.entry.LastReviewID = "legacy-review"
	legacy := runtimeRecord{Entry: journalEntry(j.entry)}
	legacy.MAC = b.signRuntime(legacy.Entry, "")
	data, _ := json.Marshal(legacy)
	b.mu.Unlock()
	b.Close()
	path := filepath.Join(cfg.StateDir, "codex-runtime-v1.jsonl")
	if err = os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	cfg.Runtime.StartClient = func(context.Context, string, string) (*Client, error) {
		t.Fatal("legacy record dispatched")
		return nil, nil
	}
	b, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	waitRuntimeInactive(t, b, original.JobID)
	old, err := b.Job(r.DeviceID, original.JobID)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := b.Submit(context.Background(), r)
	if err != nil || !reflect.DeepEqual(old, retry) {
		t.Fatalf("legacy retry changed: %+v %v", retry, err)
	}
	_, err = b.Continue(context.Background(), model.CodexContinue{DeviceID: r.DeviceID, JobID: old.JobID, RequestID: "retired-continue", Prompt: "continue"})
	if err == nil || err.Error() != "feature_retired" {
		t.Fatalf("legacy continued: %v", err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("read/retry changed legacy journal")
	}
	cancelled, err := b.Cancel(r.DeviceID, old.JobID)
	if err != nil || !cancelled.CancelRequested {
		t.Fatalf("legacy cancellation unavailable: %v", err)
	}
}

func TestLegacyApprovalProfilesBlockedWithoutMigration(t *testing.T) {
	for _, policy := range []string{"", "on-request"} {
		t.Run(policy, func(t *testing.T) {
			peer := &runtimePeer{}
			cfg, r := runtimeFixture(t, peer)
			terminalPolicy(t, cfg, policy)
			b, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			j, err := b.Submit(context.Background(), r)
			if err != nil || j.State != "blocked" || j.Code != "needs_local_setup" {
				t.Fatalf("legacy policy ran: %+v %v", j, err)
			}
			if peer.count("initialize") != 0 {
				t.Fatal("legacy policy contacted Codex")
			}
		})
	}
}
