package codexhistory

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/codexbridge"
)

func TestHistoryReadFailureInvalidatesReadyAndExplicitCheckRecovers(t *testing.T) {
	s, _ := fixture(t, func(string, any) (any, error) { return map[string]any{"data": []any{}}, nil })
	ctx := context.Background()
	if err := s.Check(ctx); err != nil {
		t.Fatal(err)
	}
	working := s.open
	s.open = func(context.Context) (RPC, io.Closer, error) { return nil, nil, errors.New("private source missing") }
	if _, err := s.Threads(ctx, ListRequest{}); err == nil {
		t.Fatal("failed source accepted")
	}
	if cap := s.Capability(); cap.Available || cap.Code != "history_local_interface_unavailable" {
		t.Fatal(cap)
	}
	s.open = working
	if _, err := s.Threads(ctx, ListRequest{}); err != nil {
		t.Fatal(err)
	}
	if s.Capability().Available {
		t.Fatal("partial read replaced explicit check")
	}
	if err := s.Check(ctx); err != nil || !s.Capability().Available {
		t.Fatal("check failed", err)
	}
}

func TestHistoryRPCFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		err         error
		unavailable bool
	}{
		{&codexbridge.TransportError{Code: "codex_connection_lost"}, true},
		{&codexbridge.RPCError{Code: -32601}, true},
		{&codexbridge.RPCError{Code: -32603}, true},
		{&codexbridge.RPCError{Code: -32602}, false},
		{&codexbridge.RPCError{Code: -32000}, false},
		{io.EOF, true},
	} {
		errNow := error(nil)
		s, _ := fixture(t, func(string, any) (any, error) { return map[string]any{"data": []any{}}, errNow })
		if err := s.Check(context.Background()); err != nil {
			t.Fatal(err)
		}
		errNow = tc.err
		if _, err := s.Threads(context.Background(), ListRequest{}); err == nil {
			t.Fatal("expected read failure")
		}
		if s.Capability().Available == tc.unavailable {
			t.Fatalf("%v: %+v", tc.err, s.Capability())
		}
	}
}

func TestHistorySourceChangeFencesLateChecksAndFailures(t *testing.T) {
	s, _ := fixture(t, func(string, any) (any, error) { return map[string]any{"data": []any{}}, nil })
	ctx := context.Background()
	if err := s.Check(ctx); err != nil {
		t.Fatal(err)
	}
	old, closed, err := s.connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer closed.Close()
	// Capture an old interface that fails after the replacement has been checked.
	r := old.(observedRPC)
	r.RPC = fakeRPC(func(string, any) (any, error) { return nil, io.EOF })
	s.Invalidate()
	if cap := s.Capability(); cap.Available || cap.State != "not_checked" {
		t.Fatal(cap)
	}
	if err := s.Check(ctx); err != nil {
		t.Fatal(err)
	}
	_ = r.Call(ctx, "thread/list", nil, nil)
	if !s.Capability().Available {
		t.Fatal("old source invalidated new check")
	}

	entered, release := make(chan struct{}), make(chan struct{})
	working := s.open
	s.open = func(context.Context) (RPC, io.Closer, error) {
		close(entered)
		<-release
		return fakeRPC(func(string, any) (any, error) { return map[string]any{"data": []any{}}, nil }), closer{}, nil
	}
	done := make(chan error, 1)
	go func() { done <- s.Check(ctx) }()
	<-entered
	s.Invalidate()
	close(release)
	if err := <-done; err == nil || err.Error() != "history_check_superseded" {
		t.Fatal(err)
	}
	if s.Capability().Available {
		t.Fatal("old check restored ready")
	}
	s.open = working
}

func TestHistoryCancelledRequestDoesNotMarkInterfaceFailed(t *testing.T) {
	s, _ := fixture(t, func(string, any) (any, error) { return map[string]any{"data": []any{}}, nil })
	if err := s.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _ = s.Threads(ctx, ListRequest{})
	if !s.Capability().Available {
		t.Fatal("caller cancellation marked source broken")
	}
}

func TestHistoryInternalDeadlineInvalidatesButCallerDeadlineDoesNot(t *testing.T) {
	for _, opening := range []bool{false, true} {
		for _, callerDeadline := range []bool{false, true} {
			t.Run(fmtHealthCase(opening, callerDeadline), func(t *testing.T) {
				s, _ := fixture(t, func(string, any) (any, error) { return map[string]any{"data": []any{}}, nil })
				if err := s.Check(context.Background()); err != nil {
					t.Fatal(err)
				}
				// Expire only after entering Open/Call; no scheduler-dependent 1ms race.
				deadline := &controlledDeadline{Context: context.Background(), delayedError: callerDeadline}
				var parent context.Context = context.Background()
				if callerDeadline {
					parent = deadline
				}
				ctx := context.WithValue(deadline, historyCallerKey{}, parent)
				if opening {
					s.open = func(context.Context) (RPC, io.Closer, error) {
						deadline.expired = true
						return nil, nil, context.DeadlineExceeded
					}
					_, _, _ = s.connect(ctx)
				} else {
					rpc, closed, err := s.connect(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer closed.Close()
					observed := rpc.(observedRPC)
					observed.RPC = fakeRPC(func(string, any) (any, error) { deadline.expired = true; return nil, context.DeadlineExceeded })
					_ = observed.Call(ctx, "thread/list", nil, nil)
				}
				if s.Capability().Available != callerDeadline {
					t.Fatalf("%+v", s.Capability())
				}
			})
		}
	}
}

// Simulates an elapsed deadline, including a delayed cancellation callback.
type controlledDeadline struct {
	context.Context
	expired, delayedError bool
}

func (c *controlledDeadline) Deadline() (time.Time, bool) {
	if c.expired {
		return time.Unix(1, 0), true
	}
	return time.Time{}, false
}
func (c *controlledDeadline) Err() error {
	if c.expired && !c.delayedError {
		return context.DeadlineExceeded
	}
	return nil
}
func fmtHealthCase(opening, caller bool) string {
	return map[bool]string{true: "open", false: "read"}[opening] + "/" + map[bool]string{true: "caller-deadline", false: "internal-deadline"}[caller]
}

func TestCancelledCheckDoesNotHideExistingReadFailure(t *testing.T) {
	s, _ := fixture(t, func(string, any) (any, error) { return map[string]any{"data": []any{}}, nil })
	if err := s.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	rpc, closed, err := s.connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer closed.Close()
	observed := rpc.(observedRPC)
	observed.RPC = fakeRPC(func(string, any) (any, error) { return nil, io.EOF })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Check(ctx); err == nil {
		t.Fatal("cancelled check succeeded")
	}
	_ = observed.Call(context.Background(), "thread/list", nil, nil)
	if s.Capability().Available {
		t.Fatal("cancelled check hid a real failure")
	}
}

func TestHistoryLateSuccessfulCheckCannotUndoReadFailure(t *testing.T) {
	s, _ := fixture(t, func(string, any) (any, error) { return map[string]any{"data": []any{}}, nil })
	if err := s.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	old, closed, err := s.connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer closed.Close()
	broken := old.(observedRPC)
	broken.RPC = fakeRPC(func(string, any) (any, error) { return nil, io.EOF })
	entered, release := make(chan struct{}), make(chan struct{})
	s.open = func(context.Context) (RPC, io.Closer, error) {
		close(entered)
		<-release
		return fakeRPC(func(string, any) (any, error) { return map[string]any{"data": []any{}}, nil }), closer{}, nil
	}
	done := make(chan error, 1)
	go func() { done <- s.Check(context.Background()) }()
	<-entered
	_ = broken.Call(context.Background(), "thread/list", nil, nil)
	close(release)
	if err := <-done; err == nil {
		t.Fatal("stale check succeeded")
	}
	if s.Capability().Available {
		t.Fatal("stale check restored readiness")
	}
}
