package codexbridge

import (
	"context"
	"encoding/json"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/engine"
	"github.com/dotcontrol/dotcontrol/internal/model"
)

type changingRuntimeMonitor struct {
	mu       sync.Mutex
	activity model.Activity
}

func (m *changingRuntimeMonitor) Snapshot() model.Activity {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.activity
}
func (m *changingRuntimeMonitor) set(a model.Activity) { m.mu.Lock(); m.activity = a; m.mu.Unlock() }

func TestNativeBusyWaitIsCancellableWithoutStartingOwnedThread(t *testing.T) {
	p := &runtimePeer{native: true, cuPresent: true}
	cfg, r := runtimeFixture(t, p)
	configureNative(t, &cfg)
	m := &changingRuntimeMonitor{activity: model.Activity{Available: true}}
	e, err := engine.New(engine.Config{DeviceID: "device-a", StateDir: t.TempDir()}, runtimeDriver{}, m)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	cfg.Runtime.Engine = e
	b, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	j, err := b.Submit(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.State == "waiting_for_idle" })
	if p.count("thread/start") != 0 || p.count("turn/start") != 0 {
		t.Fatal("dispatched while human active")
	}
	if _, err = b.Cancel(r.DeviceID, j.JobID); err != nil {
		t.Fatal(err)
	}
	waitRuntimeInactive(t, b, j.JobID)
	j, _ = b.Job(r.DeviceID, j.JobID)
	if j.State != "cancelled" {
		t.Fatalf("wait cancellation: %+v", j)
	}
}

func TestVisibleOpensOwnedThreadOnlyAfterTerminalAndCleanup(t *testing.T) {
	p := &runtimePeer{native: true, cuPresent: true}
	cfg, r := runtimeFixture(t, p)
	configureNative(t, &cfg)
	r.Presentation = "visible"
	opened := make(chan string, 2)
	var b *Bridge
	cfg.Runtime.OpenThread = func(ctx context.Context, id string) error {
		if _, err := cfg.Runtime.Engine.Apps(ctx); err != nil {
			t.Errorf("lease still held at open: %v", err)
		}
		b.mu.Lock()
		for _, job := range b.runtime.jobs {
			if job.client != nil || job.lease != nil {
				t.Error("client or lease still attached at open")
			}
		}
		b.mu.Unlock()
		opened <- id
		return nil
	}
	var err error
	b, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	j, err := b.Submit(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID != "" })
	select {
	case <-opened:
		t.Fatal("opened before turn completion")
	default:
	}
	p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "completed"}})
	waitRuntimeInactive(t, b, j.JobID)
	select {
	case id := <-opened:
		if id != j.ThreadID {
			t.Fatal("opened unrelated thread")
		}
	default:
		t.Fatal("completion not presented")
	}
}

func TestVisibleCancellationNeverOpensThread(t *testing.T) {
	p := &runtimePeer{}
	cfg, r := runtimeFixture(t, p)
	r.Presentation = "visible"
	var opened atomic.Int32
	cfg.Runtime.OpenThread = func(context.Context, string) error { opened.Add(1); return nil }
	b, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	j, err := b.Submit(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID != "" })
	b.Cancel(r.DeviceID, j.JobID)
	p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "completed"}})
	waitRuntimeInactive(t, b, j.JobID)
	if opened.Load() != 0 {
		t.Fatal("cancelled job opened popup")
	}
}

type localReviewer interface {
	ReviewLocal(context.Context, string, string, string, HumanReview) (model.CodexJob, error)
}

func requireLocalReviewer(t *testing.T, b *Bridge) localReviewer {
	t.Helper()
	v, ok := any(b).(localReviewer)
	if !ok {
		t.Fatal("trusted local review API missing")
	}
	return v
}

func TestLocalTerminalReviewWorksWithoutRemoteAcceptance(t *testing.T) {
	b, j, approval, _ := isolatedRuntimeReview(t)
	b.mu.Lock()
	b.runtime.cfg.Acceptance = map[string]PlatformAcceptance{runtime.GOOS: {}}
	b.mu.Unlock()
	local := requireLocalReviewer(t, b)
	called := false
	_, err := local.ReviewLocal(context.Background(), "device-a", j.entry.Job.JobID, approval, func(ctx context.Context, p model.ReviewPrompt) (model.ReviewAnswer, error) {
		called = true
		return acceptingReview(ctx, p)
	})
	if err != nil || !called {
		t.Fatalf("local approval unavailable: %v", err)
	}
}

func TestLocalReviewSharesOneTimeClaimWithRemoteAndRejectsStaleAnswer(t *testing.T) {
	b, j, approval, _ := isolatedRuntimeReview(t)
	local := requireLocalReviewer(t, b)
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := local.ReviewLocal(context.Background(), "device-a", j.entry.Job.JobID, approval, func(ctx context.Context, p model.ReviewPrompt) (model.ReviewAnswer, error) {
			close(entered)
			<-release
			return acceptingReview(ctx, p)
		})
		done <- err
	}()
	<-entered
	if _, err := b.Review(context.Background(), "device-a", j.entry.Job.JobID, approval, acceptingReview); err == nil || err.Error() != "approval_stale" {
		t.Fatalf("competing reviewer entered: %v", err)
	}
	b.Cancel("device-a", j.entry.Job.JobID)
	close(release)
	if err := <-done; err == nil || err.Error() != "approval_stale" {
		t.Fatalf("cancelled human response sent: %v", err)
	}
}

func TestLocalNativeReviewRequiresStopAndTakeover(t *testing.T) {
	b, j, approval, _ := isolatedRuntimeReview(t)
	local := requireLocalReviewer(t, b)
	b.mu.Lock()
	j.entry.Job.Runtime.Execution = "computer_use"
	b.mu.Unlock()
	called := false
	_, err := local.ReviewLocal(context.Background(), "device-a", j.entry.Job.JobID, approval, func(context.Context, model.ReviewPrompt) (model.ReviewAnswer, error) {
		called = true
		return model.ReviewAnswer{}, nil
	})
	if err == nil || err.Error() != "review_on_target_required" || called {
		t.Fatalf("native response bypassed handoff: %v", err)
	}
}

type localTakeover interface {
	TakeOverLocal(context.Context, string, string, string) (model.CodexJob, error)
}

func requireLocalTakeover(t *testing.T, b *Bridge) localTakeover {
	t.Helper()
	v, ok := any(b).(localTakeover)
	if !ok {
		t.Fatal("trusted local takeover API missing")
	}
	return v
}

func TestLocalTakeoverWaitsForOfficialTerminalEvidenceAndCleanup(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(map[bool]string{false: "terminal", true: "native"}[native], func(t *testing.T) {
			p := &runtimePeer{native: native, cuPresent: true}
			cfg, r := runtimeFixture(t, p)
			if native {
				configureNative(t, &cfg)
			}
			opened := make(chan string, 1)
			cfg.Runtime.OpenThread = func(ctx context.Context, id string) error {
				if native {
					if _, err := cfg.Runtime.Engine.Apps(ctx); err != nil {
						t.Errorf("lease held on handoff: %v", err)
					}
				}
				opened <- id
				return nil
			}
			b, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			takeover := requireLocalTakeover(t, b)
			j, err := b.Submit(context.Background(), r)
			if err != nil {
				t.Fatal(err)
			}
			j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID != "" })
			approval := pendingRuntimeReview(t, b, p, j)
			j, err = takeover.TakeOverLocal(context.Background(), r.DeviceID, j.JobID, approval)
			if err != nil || j.State != "awaiting_target" {
				t.Fatalf("handoff intent missing: %+v %v", j, err)
			}
			waitRuntime(t, b, j.JobID, func(model.CodexJob) bool { return p.count("turn/interrupt") == 1 })
			select {
			case <-opened:
				t.Fatal("interrupt ACK treated as stop")
			default:
			}
			if _, err = takeover.TakeOverLocal(context.Background(), r.DeviceID, j.JobID, approval); err == nil {
				t.Fatal("duplicate takeover accepted")
			}
			p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "interrupted"}})
			waitRuntimeInactive(t, b, j.JobID)
			j, _ = b.Job(r.DeviceID, j.JobID)
			if j.State != "local_takeover" || j.Runtime.PresentationState != "open_requested" || j.CancelRequested {
				t.Fatalf("handoff state: %+v %+v", j, j.Runtime)
			}
			select {
			case id := <-opened:
				if id != j.ThreadID {
					t.Fatal("wrong target")
				}
			default:
				t.Fatal("target not opened after stop")
			}
		})
	}
}

func TestLocalTakeoverUnconfirmedStopKeepsGateClosed(t *testing.T) {
	p := &runtimePeer{native: true, cuPresent: true}
	cfg, r := runtimeFixture(t, p)
	configureNative(t, &cfg)
	var opened atomic.Int32
	cfg.Runtime.OpenThread = func(context.Context, string) error { opened.Add(1); return nil }
	b, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	takeover := requireLocalTakeover(t, b)
	j, err := b.Submit(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID != "" })
	approval := pendingRuntimeReview(t, b, p, j)
	if _, err = takeover.TakeOverLocal(context.Background(), r.DeviceID, j.JobID, approval); err != nil {
		t.Fatal(err)
	}
	waitRuntimeInactive(t, b, j.JobID)
	j, _ = b.Job(r.DeviceID, j.JobID)
	if j.State != "outcome_unknown" || opened.Load() != 0 {
		t.Fatalf("uncertain stop handed off: %+v", j)
	}
	if _, err = cfg.Runtime.Engine.Apps(context.Background()); err == nil || err.Error() != "external_execution_unresolved" {
		t.Fatalf("uncertain stop lost lease: %v", err)
	}
}

func TestLocalTakeoverCancelSuppressesOpen(t *testing.T) {
	p := &runtimePeer{}
	cfg, r := runtimeFixture(t, p)
	var opened atomic.Int32
	cfg.Runtime.OpenThread = func(context.Context, string) error { opened.Add(1); return nil }
	b, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	takeover := requireLocalTakeover(t, b)
	j, err := b.Submit(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID != "" })
	approval := pendingRuntimeReview(t, b, p, j)
	if _, err = takeover.TakeOverLocal(context.Background(), r.DeviceID, j.JobID, approval); err != nil {
		t.Fatal(err)
	}
	b.Cancel(r.DeviceID, j.JobID)
	p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "interrupted"}})
	waitRuntimeInactive(t, b, j.JobID)
	if opened.Load() != 0 {
		t.Fatal("explicit cancellation opened local target")
	}
}

func TestRuntimeEvidenceDoesNotClaimImageDisplayOrReviewLocationWithoutEvidence(t *testing.T) {
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
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID != "" })
	p.notification("item/completed", map[string]any{"threadId": j.ThreadID, "turnId": j.TurnID, "item": map[string]any{"id": "image-status", "type": "mcpToolCall", "result": map[string]any{"content": []any{map[string]any{"type": "image", "mimeType": "image/png", "data": "iVBORw0KGgo="}}}}})
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return len(j.Runtime.Images) == 1 })
	raw, _ := json.Marshal(j.Runtime)
	var fields map[string]any
	json.Unmarshal(raw, &fields)
	if fields["image_delivery_status"] != "received_in_band" {
		t.Fatalf("image evidence missing: %s", raw)
	}
	if fields["review_location"] != nil {
		t.Fatal("invented review location")
	}
	p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "completed"}})
}

func TestLocalEmptyObjectAppFormHasBoundHumanReviewLocation(t *testing.T) {
	p := &runtimePeer{}
	cfg, r := runtimeFixture(t, p)
	cfg.Runtime.Acceptance[runtime.GOOS] = PlatformAcceptance{Terminal: true}
	b, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	local := requireLocalReviewer(t, b)
	j, err := b.Submit(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID != "" })
	p.send(map[string]any{"id": "app-request", "method": "mcpServer/elicitation/request", "params": map[string]any{"threadId": j.ThreadID, "turnId": j.TurnID, "serverName": "computer-use", "mode": "form", "message": "Allow Codex to use calculator", "requestedSchema": map[string]any{"type": "object", "properties": map[string]any{}}}})
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return len(j.Runtime.Approvals) == 1 })
	_, err = local.ReviewLocal(context.Background(), r.DeviceID, j.JobID, j.Runtime.Approvals[0].ApprovalID, func(ctx context.Context, p model.ReviewPrompt) (model.ReviewAnswer, error) {
		view, _ := b.Job(r.DeviceID, j.JobID)
		raw, _ := json.Marshal(view.Runtime)
		var fields map[string]any
		json.Unmarshal(raw, &fields)
		if fields["review_location"] != "local_web" {
			t.Fatalf("missing actual local review route: %s", raw)
		}
		return model.ReviewAnswer{Action: "accept", Content: map[string]any{}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "completed"}})
}

func TestVisibleCancelledDuringClientCleanupDoesNotOpen(t *testing.T) {
	p := &runtimePeer{}
	cfg, r := runtimeFixture(t, p)
	r.Presentation = "visible"
	var opened atomic.Int32
	cfg.Runtime.OpenThread = func(context.Context, string) error { opened.Add(1); return nil }
	start := cfg.Runtime.StartClient
	closing, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	cfg.Runtime.StartClient = func(ctx context.Context, exe, cwd string) (*Client, error) {
		c, err := start(ctx, exe, cwd)
		if err == nil {
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
	j, err := b.Submit(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID != "" })
	p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "completed"}})
	select {
	case <-closing:
	case <-time.After(time.Second):
		t.Fatal("no close")
	}
	if opened.Load() != 0 {
		t.Fatal("opened before client closed")
	}
	b.Cancel(r.DeviceID, j.JobID)
	unblock()
	waitRuntimeInactive(t, b, j.JobID)
	if opened.Load() != 0 {
		t.Fatal("opened after cleanup cancellation")
	}
}

type localJobLister interface {
	LocalJobs(string) ([]model.CodexJob, error)
}

func TestLocalJobsListsOnlyOwnedRuntimeSummaries(t *testing.T) {
	b, j, _, _ := isolatedRuntimeReview(t)
	b.mu.Lock()
	j.entry.Job.Runtime.Text = "private result"
	j.entry.Job.Runtime.Images = []model.Image{{Data: "private bytes"}}
	j.entry.Job.Runtime.Evidence = []model.CodexEvidence{{Output: "private output"}}
	b.mu.Unlock()
	lister, ok := any(b).(localJobLister)
	if !ok {
		t.Fatal("local owned job inventory API missing")
	}
	if _, err := lister.LocalJobs("foreign"); err == nil {
		t.Fatal("wrong device inventoried")
	}
	jobs, err := lister.LocalJobs("device-a")
	if err != nil || len(jobs) != 1 || jobs[0].JobID != j.entry.Job.JobID {
		t.Fatalf("owned summaries: %+v %v", jobs, err)
	}
	if jobs[0].Runtime.Text != "" || len(jobs[0].Runtime.Images) != 0 || len(jobs[0].Runtime.Evidence) != 0 || len(jobs[0].Runtime.Approvals) != 1 {
		t.Fatal("inventory leaked result bodies or dropped bound approval")
	}
	jobs[0].Runtime.Approvals = nil
	got, _ := b.Job("device-a", j.entry.Job.JobID)
	if len(got.Runtime.Approvals) != 1 {
		t.Fatal("inventory alias changed job")
	}
}

func TestTerminalEvidenceLatchesCancelledNativeLeaseBeforeCleanup(t *testing.T) {
	b, j, _, _ := isolatedRuntimeReview(t)
	lease, err := b.runtime.cfg.Engine.AcquireDelegation(context.Background(), j.entry.Job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Finish(true)
	b.mu.Lock()
	j.lease = lease
	j.entry.Job.Runtime.Execution = "computer_use"
	b.mu.Unlock()
	if err = b.runtime.cfg.Engine.Pause(true); err != nil {
		t.Fatal(err)
	}
	if err = b.runtime.cfg.Engine.Pause(false); err != nil {
		t.Fatal(err)
	}
	// A terminal notification can win the consume select before its cancelled
	// lease is handled. Cleanup must not erase this human interruption evidence.
	b.finishTurn(j.entry.Job.JobID, "completed")
	got, _ := b.Job("device-a", j.entry.Job.JobID)
	if !got.CancelRequested {
		t.Fatal("terminal event lost native interruption before cleanup")
	}
}

func isolatedCompetingHandoff(t *testing.T) (*Bridge, *runtimeJob, string, string) {
	t.Helper()
	b, j, first, _ := isolatedRuntimeReview(t)
	b.mu.Lock()
	j.handoff = make(chan struct{})
	b.runtime.cfg.OpenThread = func(context.Context, string) error { return nil }
	request := j.approvals[first].request
	request.ID = json.RawMessage(`"second-approval"`)
	c := j.client
	b.mu.Unlock()
	c.mu.Lock()
	c.serverRequests[string(request.ID)] = request
	c.mu.Unlock()
	b.refreshApprovals(j.entry.Job.JobID, c)
	b.mu.Lock()
	defer b.mu.Unlock()
	for id := range j.approvals {
		if id != first {
			return b, j, first, id
		}
	}
	t.Fatal("second owned approval missing")
	return nil, nil, "", ""
}

func TestReviewAnswerCannotRespondAfterDistinctApprovalRequestsTakeover(t *testing.T) {
	for _, local := range []bool{false, true} {
		t.Run(map[bool]string{false: "remote", true: "local"}[local], func(t *testing.T) {
			b, j, first, second := isolatedCompetingHandoff(t)
			entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			go func() {
				human := func(ctx context.Context, p model.ReviewPrompt) (model.ReviewAnswer, error) {
					close(entered)
					<-release
					return acceptingReview(ctx, p)
				}
				_, err := b.review(context.Background(), "device-a", j.entry.Job.JobID, first, human, local)
				done <- err
			}()
			<-entered
			if _, err := b.TakeOverLocal(context.Background(), "device-a", j.entry.Job.JobID, second); err != nil {
				close(release)
				t.Fatal(err)
			}
			close(release)
			if err := <-done; err == nil || err.Error() != "approval_stale" {
				t.Fatalf("stale answer sent after takeover: %v", err)
			}
			b.mu.Lock()
			defer b.mu.Unlock()
			if j.entry.Phase != "local_takeover_intent" || j.entry.Job.Code != "interrupt_for_local_takeover" || j.approvals[first].consumed {
				t.Fatal("old review replaced handoff intent")
			}
			if !requestPending(j.client, j.approvals[first].request) {
				t.Fatal("old approval was responded to")
			}
		})
	}
}

func TestTakeoverCancelsAlreadyBlockedReviewWrite(t *testing.T) {
	b, j, first, second := isolatedCompetingHandoff(t)
	gate := gateReviewWriter(t, b, j.entry.Job.JobID)
	done := make(chan error, 1)
	go func() {
		_, err := b.Review(context.Background(), "device-a", j.entry.Job.JobID, first, acceptingReview)
		done <- err
	}()
	<-gate.entered
	if _, err := b.TakeOverLocal(context.Background(), "device-a", j.entry.Job.JobID, second); err != nil {
		gate.Release()
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil || err.Error() != "review_response_outcome_unknown" {
			t.Fatalf("in-flight response falsely confirmed: %v", err)
		}
	case <-time.After(250 * time.Millisecond):
		gate.Release()
		<-done
		t.Fatal("handoff left old review writer active")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if j.entry.Phase != "local_takeover_intent" || j.entry.Job.Code != "interrupt_for_local_takeover" {
		t.Fatal("late response overwrote takeover")
	}
}
