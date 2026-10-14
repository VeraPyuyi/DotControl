package codexbridge

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

func TestVisibleOpensOwnedThreadOnlyAfterTerminalAndCleanup(t *testing.T) {
	p := &runtimePeer{}
	cfg, r := runtimeFixture(t, p)
	r.Presentation = "visible"
	opened := make(chan string, 2)
	var b *Bridge
	cfg.Runtime.OpenThread = func(ctx context.Context, id string) error {
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
