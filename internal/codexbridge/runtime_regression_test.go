package codexbridge

import (
	"bufio"
	"context"
	"encoding/json"
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
	cfg.Runtime.Acceptance = map[string]PlatformAcceptance{runtime.GOOS: {}}
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
		if len(c.PendingRequests()) > 0 {
			return b, j, "pending", cancel
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("pending request not received")
	return nil, nil, "", nil
}
