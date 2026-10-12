package codexbridge

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/engine"
	"github.com/dotcontrol/dotcontrol/internal/model"
)

func waitRuntimeInactive(t *testing.T, b *Bridge, id string) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		active := b.runtime.jobs[id].active
		b.mu.Unlock()
		if !active {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("runtime cleanup did not finish")
}

func TestContinueIntentRecoveryCannotCompleteFromPreviousTurn(t *testing.T) {
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
	p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "completed"}})
	waitRuntimeInactive(t, b, j.JobID)
	continuation := model.CodexContinue{DeviceID: r.DeviceID, JobID: j.JobID, RequestID: "next-request", Prompt: "Continue the owned task"}
	if _, err = b.Continue(context.Background(), continuation); err != nil {
		t.Fatal(err)
	}
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID == "turn-2" })
	p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "completed"}})
	waitRuntimeInactive(t, b, j.JobID)
	if err = b.Close(); err != nil {
		t.Fatal(err)
	}

	// Replay exactly the authenticated bytes durable at the continuation intent,
	// simulating a crash before any RPC or new turn binding for that request.
	ledger, err := os.ReadFile(filepath.Join(cfg.StateDir, "codex-runtime-v1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var snapshot strings.Builder
	found := false
	for _, line := range strings.SplitAfter(string(ledger), "\n") {
		var record runtimeRecord
		if json.Unmarshal([]byte(line), &record) != nil {
			continue
		}
		snapshot.WriteString(line)
		if record.Entry.CurrentRequestID == continuation.RequestID && record.Entry.Phase == "continue_intent" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("continuation intent missing from journal")
	}
	crashDir := t.TempDir()
	for _, name := range []string{"codex-jobs.jsonl", "codex-jobs.key"} {
		data, e := os.ReadFile(filepath.Join(cfg.StateDir, name))
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(crashDir, name), data, 0600); e != nil {
			t.Fatal(e)
		}
	}
	if err = os.WriteFile(filepath.Join(crashDir, "codex-runtime-v1.jsonl"), []byte(snapshot.String()), 0600); err != nil {
		t.Fatal(err)
	}
	cfg.StateDir = crashDir
	recovered, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	waitRuntimeInactive(t, recovered, j.JobID)
	j, err = recovered.Job(r.DeviceID, j.JobID)
	if err != nil || j.State != "outcome_unknown" || j.Code != "turn_binding_unknown" || j.TurnID != "" {
		t.Fatalf("previous turn falsely resolved queued continuation: %+v, %v", j, err)
	}
	if p.count("turn/start") != 2 || p.count("thread/read") != 1 {
		t.Fatal("recovery replayed a turn or did not read its owned thread")
	}
}

func TestContinueWaitsUntilPreviousClientCloseFinishes(t *testing.T) {
	p := &runtimePeer{}
	cfg, r := runtimeFixture(t, p)
	start := cfg.Runtime.StartClient
	closing, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseClose := func() { releaseOnce.Do(func() { close(release) }) }
	first := true
	cfg.Runtime.StartClient = func(ctx context.Context, executable, cwd string) (*Client, error) {
		c, err := start(ctx, executable, cwd)
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
	defer releaseClose()
	j, err := b.Submit(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID == "turn-1" })
	p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "completed"}})
	select {
	case <-closing:
	case <-time.After(time.Second):
		t.Fatal("client close was not reached")
	}
	continuation := model.CodexContinue{DeviceID: r.DeviceID, JobID: j.JobID, RequestID: "after-close", Prompt: "Continue the owned task"}
	if _, err = b.Continue(context.Background(), continuation); err == nil || err.Error() != "job_not_continuable" {
		t.Fatalf("continuation allowed before previous client cleanup: %v", err)
	}
	if p.count("thread/resume") != 0 {
		t.Fatal("resumed while previous writer was closing")
	}
	releaseClose()
	waitRuntimeInactive(t, b, j.JobID)
	if _, err = b.Continue(context.Background(), continuation); err != nil {
		t.Fatal(err)
	}
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID == "turn-2" })
	p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "completed"}})
	waitRuntimeInactive(t, b, j.JobID)
}

func TestDelayedCleanupCannotClearReplacementRun(t *testing.T) {
	p := &runtimePeer{}
	cfg, r := runtimeFixture(t, p)
	start := cfg.Runtime.StartClient
	closing, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseClose := func() { releaseOnce.Do(func() { close(release) }) }
	cfg.Runtime.StartClient = func(ctx context.Context, executable, cwd string) (*Client, error) {
		c, err := start(ctx, executable, cwd)
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
	defer releaseClose()
	j, err := b.Submit(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID == "turn-1" })
	p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "completed"}})
	select {
	case <-closing:
	case <-time.After(time.Second):
		t.Fatal("client close was not reached")
	}
	// Simulate replacement ownership to independently test the cleanup binding;
	// normal Continue must already reject this transition until cleanup completes.
	replacement, cancel := context.WithCancel(context.Background())
	defer cancel()
	replacementClient := &Client{}
	b.mu.Lock()
	owned := b.runtime.jobs[j.JobID]
	owned.active = true
	owned.runContext = replacement
	owned.cancel = cancel
	owned.client = replacementClient
	owned.entry.CurrentRequestID = "replacement-request"
	owned.entry.Job.TurnID = "replacement-turn"
	owned.entry.Job.State = "running"
	owned.entry.Job.Code = "replacement-running"
	b.mu.Unlock()
	releaseClose()
	b.runtime.wg.Wait()
	b.mu.Lock()
	defer b.mu.Unlock()
	if !owned.active || owned.runContext != replacement || owned.client != replacementClient || replacement.Err() != nil || owned.entry.Job.Code != "replacement-running" || owned.entry.Job.TurnID != "replacement-turn" {
		t.Fatal("old cleanup modified replacement run ownership")
	}
}

type reviewGateWriter struct {
	inner                  io.WriteCloser
	entered                chan struct{}
	release                chan struct{}
	enterOnce, releaseOnce sync.Once
}

func (w *reviewGateWriter) Write(data []byte) (int, error) {
	if strings.Contains(string(data), `"id":"review-rpc"`) {
		w.enterOnce.Do(func() { close(w.entered) })
		<-w.release
	}
	return w.inner.Write(data)
}
func (w *reviewGateWriter) Release()     { w.releaseOnce.Do(func() { close(w.release) }) }
func (w *reviewGateWriter) Close() error { w.Release(); return w.inner.Close() }

func pendingRuntimeReview(t *testing.T, b *Bridge, p *runtimePeer, j model.CodexJob) string {
	t.Helper()
	p.send(map[string]any{"id": "review-rpc", "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": j.ThreadID, "turnId": j.TurnID, "itemId": "review-command", "command": "echo review", "cwd": t.TempDir(), "startedAtMs": time.Now().UnixMilli()}})
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return len(j.Runtime.Approvals) == 1 })
	return j.Runtime.Approvals[0].ApprovalID
}
func acceptingReview(context.Context, model.ReviewPrompt) (model.ReviewAnswer, error) {
	return model.ReviewAnswer{Action: "accept", Content: map[string]any{"decision": "accept"}}, nil
}
func gateReviewWriter(t *testing.T, b *Bridge, id string) *reviewGateWriter {
	t.Helper()
	b.mu.Lock()
	c := b.runtime.jobs[id].client
	b.mu.Unlock()
	// The worker is idle awaiting review; transport serialization protects replacement.
	c.writeMu.Lock()
	w := &reviewGateWriter{inner: c.writer, entered: make(chan struct{}), release: make(chan struct{})}
	c.writer = w
	c.writeMu.Unlock()
	t.Cleanup(w.Release)
	return w
}
func TestBlockedReviewWriteDoesNotBlockCancellation(t *testing.T) {
	p := &runtimePeer{}
	cfg, r := runtimeFixture(t, p)
	b, e := New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	j, _ := b.Submit(context.Background(), r)
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID != "" })
	approval := pendingRuntimeReview(t, b, p, j)
	gate := gateReviewWriter(t, b, j.JobID)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	reviewDone := make(chan error, 1)
	go func() { _, e := b.Review(ctx, "device-a", j.JobID, approval, acceptingReview); reviewDone <- e }()
	<-gate.entered
	cancelled := make(chan model.CodexJob, 1)
	go func() { v, _ := b.Cancel("device-a", j.JobID); cancelled <- v }()
	blocked := false
	select {
	case got := <-cancelled:
		if !got.CancelRequested {
			t.Error("cancellation intent missing")
		}
	case <-time.After(250 * time.Millisecond):
		blocked = true
	}
	gate.Release()
	<-reviewDone
	if blocked {
		<-cancelled
		t.Fatal("approval write held Bridge.mu and blocked cancellation")
	}
}
func TestReviewWriteCannotOverwriteTerminalEvent(t *testing.T) {
	p := &runtimePeer{}
	cfg, r := runtimeFixture(t, p)
	b, e := New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	j, _ := b.Submit(context.Background(), r)
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID != "" })
	approval := pendingRuntimeReview(t, b, p, j)
	gate := gateReviewWriter(t, b, j.JobID)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	reviewDone := make(chan error, 1)
	go func() { _, e := b.Review(ctx, "device-a", j.JobID, approval, acceptingReview); reviewDone <- e }()
	<-gate.entered
	p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "completed"}})
	terminal := make(chan model.CodexJob, 1)
	go func() { v, _ := b.Job("device-a", j.JobID); terminal <- v }()
	blocked := false
	select {
	case <-terminal:
	case <-time.After(250 * time.Millisecond):
		blocked = true
	}
	if blocked {
		gate.Release()
		<-reviewDone
		t.Fatal("terminal processing blocked behind review write")
	}
	select {
	case <-reviewDone:
	case <-time.After(1500 * time.Millisecond):
		gate.Release()
		<-reviewDone
		t.Fatal("review write was not bounded")
	}
	got := waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.State == "completed" })
	if got.Code != "model_turn_completed" {
		t.Fatalf("review overwrote terminal code: %s", got.Code)
	}
}
func TestReviewRejectsDirectPauseBeforeRunLoopNotices(t *testing.T) {
	p := &runtimePeer{}
	cfg, r := runtimeFixture(t, p)
	local, e := engine.New(engine.Config{DeviceID: "device-a", StateDir: t.TempDir()}, runtimeDriver{}, runtimeMonitor{})
	if e != nil {
		t.Fatal(e)
	}
	defer local.Close()
	cfg.Runtime.Engine = local
	b, e := New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	j, _ := b.Submit(context.Background(), r)
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID != "" })
	approval := pendingRuntimeReview(t, b, p, j)
	// Hold the bridge lock across pausing to ensure the runtime loop has not yet
	// persisted CancelRequested when the review callback returns.
	_, e = b.Review(context.Background(), "device-a", j.JobID, approval, func(ctx context.Context, p model.ReviewPrompt) (model.ReviewAnswer, error) {
		b.mu.Lock()
		err := local.Pause(true)
		b.mu.Unlock()
		if err != nil {
			return model.ReviewAnswer{}, err
		}
		return acceptingReview(ctx, p)
	})
	if e == nil {
		t.Fatal("human answer forwarded after target pause")
	}
}
func TestRecoveredTerminalPreservesUnavailableResultStatus(t *testing.T) {
	p := &runtimePeer{}
	cfg, r := runtimeFixture(t, p)
	b, e := New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	j, _ := b.Submit(context.Background(), r)
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID != "" })
	p.notification("item/completed", map[string]any{"threadId": j.ThreadID, "turnId": j.TurnID, "item": map[string]any{"id": "message", "type": "agentMessage", "text": "private lost result"}})
	waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.Runtime.Text != "" })
	p.mu.Lock()
	c := p.conn
	p.mu.Unlock()
	c.Close()
	waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.State == "outcome_unknown" })
	b.Close()
	b, e = New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.State == "completed" })
	if j.Runtime.ResultStatus != "unavailable_after_restart" || j.Runtime.Text != "" {
		t.Fatalf("recovery claimed restored results: %+v", j.Runtime)
	}
}
func TestStartupPauseLatchesBeforeThreadResponseAndPreventsTurn(t *testing.T) {
	for _, method := range []string{"permissionProfile/list", "thread/start"} {
		t.Run(method, func(t *testing.T) {
			p := &runtimePeer{blockMethod: method, blockEntered: make(chan struct{}), blockRelease: make(chan struct{})}
			cfg, r := runtimeFixture(t, p)
			local, e := engine.New(engine.Config{DeviceID: "device-a", StateDir: t.TempDir()}, runtimeDriver{}, runtimeMonitor{})
			if e != nil {
				t.Fatal(e)
			}
			defer local.Close()
			cfg.Runtime.Engine = local
			b, e := New(cfg)
			if e != nil {
				t.Fatal(e)
			}
			defer b.Close()
			j, _ := b.Submit(context.Background(), r)
			<-p.blockEntered
			if e = local.Pause(true); e != nil {
				t.Fatal(e)
			}
			latched := false
			deadline := time.Now().Add(300 * time.Millisecond)
			for time.Now().Before(deadline) {
				got, _ := b.Job("device-a", j.JobID)
				if got.CancelRequested {
					latched = true
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			local.Pause(false)
			close(p.blockRelease)
			if !latched {
				t.Fatal("pause during startup RPC was not latched")
			}
			waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return runtimeTerminal(j.State) || j.State == "outcome_unknown" })
			if p.count("turn/start") != 0 {
				t.Fatal("new turn dispatched after startup pause")
			}
		})
	}
}

// Isolated request fixture gives the review coordinator a real pending RPC but
// no consumer loop that could mask a missing direct context/pause check.
func isolatedRuntimeReview(t *testing.T) (*Bridge, *runtimeJob, string, context.CancelFunc) {
	t.Helper()
	p := &runtimePeer{}
	cfg, r := runtimeFixture(t, p)
	cfg.Runtime.Acceptance = map[string]PlatformAcceptance{runtime.GOOS: {RemoteReview: true}}
	local, e := engine.New(engine.Config{DeviceID: "device-a", StateDir: t.TempDir()}, runtimeDriver{}, runtimeMonitor{})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { local.Close() })
	cfg.Runtime.Engine = local
	b, e := New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { b.Close() })
	job, e := b.Submit(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	run, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c := fakeClient(t, func(conn net.Conn) {
		json.NewEncoder(conn).Encode(map[string]any{"id": "review-rpc", "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": "owned-thread", "turnId": "turn-1", "itemId": "item-1", "command": "echo bounded", "cwd": "local-scope", "startedAtMs": time.Now().UnixMilli()}})
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
		}
	})
	b.mu.Lock()
	j := b.runtime.jobs[job.JobID]
	j.entry.Job.ThreadID = "owned-thread"
	j.entry.Job.TurnID = "turn-1"
	j.entry.Job.State = "running"
	j.entry.Phase = "turn_bound"
	j.client = c
	j.active = true
	j.runContext = run
	j.cancel = cancel
	b.mu.Unlock()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		b.refreshApprovals(job.JobID, c)
		view, _ := b.Job("device-a", job.JobID)
		if len(view.Runtime.Approvals) == 1 {
			return b, j, view.Runtime.Approvals[0].ApprovalID, cancel
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("approval not received")
	return nil, nil, "", nil
}
func TestReviewRejectsCancelledRunWithoutPersistedCancelFlag(t *testing.T) {
	b, j, approval, cancel := isolatedRuntimeReview(t)
	cancel()
	called := false
	_, e := b.Review(context.Background(), "device-a", j.entry.Job.JobID, approval, func(ctx context.Context, p model.ReviewPrompt) (model.ReviewAnswer, error) {
		called = true
		return acceptingReview(ctx, p)
	})
	if e == nil || called || j.entry.Job.CancelRequested {
		t.Fatal("run cancellation was not checked directly before opening human review")
	}
}
func TestReviewRejectsCancelledLeaseEvenAfterTargetResumed(t *testing.T) {
	b, j, approval, _ := isolatedRuntimeReview(t)
	lease, e := b.runtime.cfg.Engine.AcquireDelegation(context.Background(), j.entry.Job.JobID)
	if e != nil {
		t.Fatal(e)
	}
	defer lease.Finish(true)
	j.lease = lease
	_, e = b.Review(context.Background(), "device-a", j.entry.Job.JobID, approval, func(ctx context.Context, p model.ReviewPrompt) (model.ReviewAnswer, error) {
		b.runtime.cfg.Engine.Pause(true)
		b.runtime.cfg.Engine.Pause(false)
		return acceptingReview(ctx, p)
	})
	if e == nil || j.entry.Job.CancelRequested || j.runContext.Err() != nil || lease.Context.Err() == nil {
		t.Fatal("cancelled lease accepted a late human answer")
	}
}
func TestReviewWriteUsesItsOwnShortDeadline(t *testing.T) {
	b, j, approval, _ := isolatedRuntimeReview(t)
	gate := gateReviewWriter(t, b, j.entry.Job.JobID)
	done := make(chan error, 1)
	go func() {
		_, e := b.Review(context.Background(), "device-a", j.entry.Job.JobID, approval, acceptingReview)
		done <- e
	}()
	<-gate.entered
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("blocked response succeeded")
		}
	case <-time.After(1500 * time.Millisecond):
		gate.Release()
		<-done
		t.Fatal("review write lacked independent short deadline")
	}
}
func TestReviewWriteCannotOverwriteReplacementTurn(t *testing.T) {
	b, j, approval, _ := isolatedRuntimeReview(t)
	gate := gateReviewWriter(t, b, j.entry.Job.JobID)
	done := make(chan error, 1)
	go func() {
		_, e := b.Review(context.Background(), "device-a", j.entry.Job.JobID, approval, acceptingReview)
		done <- e
	}()
	<-gate.entered
	replacement, cancel := context.WithCancel(context.Background())
	defer cancel()
	b.mu.Lock()
	j.runContext = replacement
	j.entry.Job.TurnID = "turn-replacement"
	j.entry.CurrentRequestID = "request-replacement"
	j.entry.Phase = "turn_bound"
	j.entry.Job.Code = "replacement_running"
	b.mu.Unlock()
	gate.Release()
	<-done
	got, e := b.Job("device-a", j.entry.Job.JobID)
	if e != nil || got.Code != "replacement_running" || got.TurnID != "turn-replacement" {
		t.Fatalf("late review overwrote replacement: %+v %v", got, e)
	}
}
