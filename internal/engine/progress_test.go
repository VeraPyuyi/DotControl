package engine

import (
	"context"
	"encoding/json"
	"github.com/dotcontrol/dotcontrol/internal/model"
	"strings"
	"testing"
	"time"
)

func TestCancellationProgressWaitsForNativeCompletion(t *testing.T) {
	e, d, _, _ := setup(t)
	a := action(t, e, "slow-progress")
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	d.execute = func(context.Context, model.Action, model.View, string) model.Outcome {
		close(entered)
		<-release
		return model.Outcome{State: "succeeded"}
	}
	done := make(chan struct{})
	go func() { defer close(done); _, _ = e.Execute(context.Background(), a) }()
	<-entered
	if err := e.Cancel(a.TaskID); err != nil {
		t.Fatal(err)
	}
	p := e.Progress()
	if !p.CancelRegistered || !p.NativePending || p.CanObserve {
		t.Fatalf("premature completion %+v", p)
	}
	<-done
	p = e.Progress()
	if !p.NativePending || p.CanObserve {
		t.Fatalf("late native lost %+v", p)
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for e.Progress().NativePending && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	p = e.Progress()
	if p.NativePending || !p.CanObserve || p.TaskID != a.TaskID || p.ActionID != a.ActionID {
		t.Fatal(p)
	}
	if p.ExecutionMillis < 0 || p.QueueMillis < 0 {
		t.Fatal(p)
	}
	b, _ := json.Marshal(p)
	if strings.Contains(string(b), string(a.Args)) {
		t.Fatal("arguments exported")
	}
}
