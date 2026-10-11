package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type backendFunc func(context.Context, string, Request) (any, error)

func (f backendFunc) Call(c context.Context, m string, r Request) (any, error) { return f(c, m, r) }

func TestPauseCancelsInflightAndDeniesFutureActions(t *testing.T) {
	entered := make(chan struct{})
	done := make(chan error, 1)
	var actions atomic.Int32
	sw := &Switch{Backend: backendFunc(func(ctx context.Context, method string, r Request) (any, error) {
		if method == "execute" {
			actions.Add(1)
			close(entered)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return method, nil
	})}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() { _, err := sw.Call(ctx, "execute", Request{}); done <- err }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("inflight action never entered")
	}
	sw.SetPaused(true)
	if !sw.Paused() {
		t.Fatal("pause not visible")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("inflight error=%v", err)
		}
	case <-ctx.Done():
		t.Fatal("pause failed to cancel inflight context")
	}
	for _, method := range []string{"execute", "observe", "apps", "codex_submit"} {
		if _, err := sw.Call(ctx, method, Request{}); err == nil || err.Error() != "gateway_paused" {
			t.Errorf("paused %s err=%v", method, err)
		}
	}
	if actions.Load() != 1 {
		t.Fatal("paused action reached backend")
	}
	for _, method := range []string{"devices", "task", "cancel", "codex_job", "codex_cancel"} {
		if v, err := sw.Call(ctx, method, Request{}); err != nil || v != method {
			t.Errorf("paused recovery method %s: %v %v", method, v, err)
		}
	}
	sw.SetPaused(false)
	if sw.Paused() {
		t.Fatal("resume not visible")
	}
	if v, err := sw.Call(ctx, "observe", Request{}); err != nil || v != "observe" {
		t.Fatalf("resumed observation: %v %v", v, err)
	}
}

func TestSwitchPropagatesCallerCancellation(t *testing.T) {
	sw := &Switch{Backend: backendFunc(func(ctx context.Context, _ string, _ Request) (any, error) { <-ctx.Done(); return nil, ctx.Err() })}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := sw.Call(ctx, "observe", Request{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("caller cancellation=%v", err)
	}
}
