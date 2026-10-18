package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type upgradeBackend struct{ calls atomic.Int32 }

type heldUpgradeBackend struct{ entered, release chan struct{} }

func (b *heldUpgradeBackend) Call(ctx context.Context, method string, r Request) (any, error) {
	close(b.entered)
	select {
	case <-b.release:
		return nil, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestUpgradeWaitsForAdmittedCallAndRejectsConcurrentDrain(t *testing.T) {
	b := &heldUpgradeBackend{make(chan struct{}), make(chan struct{})}
	g := &UpgradeGate{Backend: b, Ready: func() error { return nil }}
	callDone := make(chan error, 1)
	go func() { _, err := g.Call(context.Background(), "codex_submit", Request{}); callDone <- err }()
	<-b.entered
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- g.Drain(ctx) }()
	for !g.Draining() {
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		time.Sleep(time.Millisecond)
	}
	if err := g.Drain(ctx); err == nil || err.Error() != "upgrade_in_progress" {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		t.Fatal("ignored admitted call", err)
	default:
	}
	close(b.release)
	if err := <-callDone; err != nil {
		t.Fatal("cancelled existing call", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func (b *upgradeBackend) Call(context.Context, string, Request) (any, error) {
	b.calls.Add(1)
	return nil, nil
}

func TestUpgradeDrainsWithoutCancellingAndRejectsNewExecution(t *testing.T) {
	b := &upgradeBackend{}
	var busy atomic.Bool
	busy.Store(true)
	g := &UpgradeGate{Backend: b, Ready: func() error {
		if busy.Load() {
			return errors.New("upgrade_busy")
		}
		return nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- g.Drain(ctx) }()
	for !g.Draining() {
		select {
		case <-ctx.Done():
			t.Fatal("not draining")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	for _, method := range []string{"execute", "codex_submit", "codex_continue"} {
		if _, err := g.Call(ctx, method, Request{}); err == nil || err.Error() != "upgrade_in_progress" {
			t.Fatal(method, err)
		}
	}
	for _, method := range []string{"devices", "codex_job", "codex_cancel"} {
		if _, err := g.Call(ctx, method, Request{}); err != nil {
			t.Fatal(method, err)
		}
	}
	select {
	case err := <-done:
		t.Fatal("stopped active work", err)
	default:
	}
	busy.Store(false)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !g.Draining() {
		t.Fatal("admission reopened before shutdown")
	}
}

func TestUpgradeCancellationAndUnknownStopReopenAdmission(t *testing.T) {
	for _, reason := range []string{"upgrade_busy", "upgrade_unresolved"} {
		g := &UpgradeGate{Backend: &upgradeBackend{}, Ready: func() error { return errors.New(reason) }}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		err := g.Drain(ctx)
		cancel()
		if err == nil || g.Draining() {
			t.Fatal("failed upgrade retained drain", err)
		}
		if _, err := g.Call(context.Background(), "codex_submit", Request{}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUpgradeCancellationDuringReadinessRestoresAdmission(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	g := &UpgradeGate{Backend: &upgradeBackend{}, Ready: func() error { cancel(); return nil }}
	if err := g.Drain(ctx); !errors.Is(err, context.Canceled) || g.Draining() {
		t.Fatalf("abandoned upgrade committed shutdown: %v", err)
	}
	if _, err := g.Call(context.Background(), "codex_submit", Request{}); err != nil {
		t.Fatal(err)
	}
}
