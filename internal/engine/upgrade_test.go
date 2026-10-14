package engine

import (
	"context"
	"github.com/dotcontrol/dotcontrol/internal/model"
	"testing"
	"time"
)

func TestUpgradeWaitsBeyondUnknownReceiptForNativeStop(t *testing.T) {
	e, d, _, _ := setup(t)
	a := action(t, e, "upgrade-a")
	started, finish := make(chan struct{}), make(chan struct{})
	d.execute = func(context.Context, model.Action, model.View, string) model.Outcome {
		close(started)
		<-finish
		return model.Outcome{State: "succeeded"}
	}
	defer close(finish)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { _, _ = e.Execute(ctx, a); close(done) }()
	<-started
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("unknown receipt not returned")
	}
	if err := e.UpgradeReady(); err == nil || err.Error() != "upgrade_busy" {
		t.Fatalf("unconfirmed stop was ready: %v", err)
	}
}

func TestUpgradeReadinessDoesNotCancelOrChangePause(t *testing.T) {
	e, _, _, _ := setup(t)
	initial := e.Device().Paused
	for _, tc := range []struct {
		active, unresolved bool
		want               string
	}{
		{want: ""}, {active: true, want: "upgrade_busy"},
		{unresolved: true, want: "upgrade_unresolved"},
	} {
		e.mu.Lock()
		if tc.active {
			e.activeDone = make(chan struct{})
		} else {
			e.activeDone = nil
		}
		if tc.unresolved {
			e.externalPending = "pending"
		} else {
			e.externalPending = ""
		}
		e.mu.Unlock()
		err := e.UpgradeReady()
		got := ""
		if err != nil {
			got = err.Error()
		}
		if got != tc.want {
			t.Fatalf("got %q want %q", got, tc.want)
		}
		if e.Device().Paused != initial {
			t.Fatal("pause changed")
		}
	}
	e.mu.Lock()
	e.activeDone = nil
	e.externalPending = ""
	e.mu.Unlock()
}
