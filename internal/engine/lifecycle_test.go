package engine

import (
	"context"
	"github.com/dotcontrol/dotcontrol/internal/model"
	"testing"
)

func TestReconnectInvalidatesOldObservationAndPreservesPauseAndReceipts(t *testing.T) {
	e, _, _, _ := setup(t)
	a := action(t, e, "old-observation")
	replacement := &testDriver{}
	if err := e.Reconnect(context.Background(), func() model.Driver { return replacement }); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Execute(context.Background(), a); err == nil {
		t.Fatal("old connection observation accepted")
	}
	if replacement.calls.Load() != 0 {
		t.Fatal("stale action dispatched")
	}
	if err := e.Pause(true); err != nil {
		t.Fatal(err)
	}
	if err := e.Reconnect(context.Background(), func() model.Driver { return &testDriver{} }); err != nil {
		t.Fatal(err)
	}
	if !e.Device().Paused {
		t.Fatal("repair resumed device")
	}
}
func TestFreshDevicePauseIsDurableInInitialRecord(t *testing.T) {
	cfg := Config{DeviceID: "fresh", Name: "Fresh", StateDir: t.TempDir(), InitialPaused: true}
	for n := 0; n < 2; n++ {
		e, err := New(cfg, &testDriver{}, &testMonitor{activity: idle()})
		if err != nil {
			t.Fatal(err)
		}
		if !e.Device().Paused {
			t.Fatal("fresh init-only ledger resumed without local enable")
		}
		if err = e.Close(); err != nil {
			t.Fatal(err)
		}
		cfg.InitialPaused = false // Startup defaults cannot rewrite the durable choice.
	}
}
func TestInvalidationRejectsCaptureAlreadyInProgress(t *testing.T) {
	e, d, _, _ := setup(t)
	entered, release := make(chan struct{}), make(chan struct{})
	d.observe = func(ctx context.Context, target model.Target) (model.View, error) {
		close(entered)
		<-release
		return model.View{Target: target}, nil
	}
	result := make(chan error, 1)
	go func() { _, err := e.Observe(context.Background(), target); result <- err }()
	<-entered
	e.Invalidate()
	close(release)
	if code(<-result) != "stale_observation" {
		t.Fatal("capture survived connection invalidation")
	}
}
