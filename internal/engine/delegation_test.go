package engine

import (
	"context"
	"errors"
	"github.com/dotcontrol/dotcontrol/internal/model"
	"os"
	"testing"
	"time"
)

func TestDelegationStopStorageFailureKeepsGateClosed(t *testing.T) {
	e, _, m, _ := setup(t)
	m.set(model.Activity{Available: true, IdleMillis: 120000})
	lease, err := e.AcquireDelegation(context.Background(), "native-storage")
	if err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	e.syncFile = func(*os.File) error { return errors.New("injected sync failure") }
	e.mu.Unlock()
	if err = lease.Finish(true); err == nil {
		t.Fatal("undurable stop accepted")
	}
	if _, err = e.Apps(context.Background()); err == nil {
		t.Fatal("storage failure opened desktop gate")
	}
}

func TestDelegationLeaseBlocksCuaAndInterruptsOnHumanInput(t *testing.T) {
	e, d, m, _ := setup(t)
	m.set(model.Activity{Available: true, IdleMillis: 120000, Generation: 1})
	lease, err := e.AcquireDelegation(context.Background(), "codex-test")
	if err != nil {
		t.Fatal(err)
	}
	before := d.calls.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err = e.Apps(ctx); err == nil {
		t.Fatal("Cua entered native lease")
	}
	if d.calls.Load() != before {
		t.Fatal("driver called")
	}
	m.set(model.Activity{Available: true, IdleMillis: 0, Generation: 2})
	select {
	case <-lease.Context.Done():
	case <-time.After(time.Second):
		t.Fatal("no takeover interruption")
	}
	if err = lease.Finish(false); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Apps(context.Background()); err == nil || err.Error() != "external_execution_unresolved" {
		t.Fatalf("unconfirmed native action released: %v", err)
	}
	if err = e.ConfirmDelegationStopped("wrong"); err == nil {
		t.Fatal("wrong task cleared guard")
	}
	if err = e.ConfirmDelegationStopped("codex-test"); err != nil {
		t.Fatal(err)
	}
}

func TestDelegationLeaseRequiresIdleUnlockedUnpausedDevice(t *testing.T) {
	e, _, m, _ := setup(t)
	for _, a := range []model.Activity{{}, {Available: true, Locked: true, IdleMillis: 120000}, {Available: true, IdleMillis: 2}} {
		m.set(a)
		if l, err := e.AcquireDelegation(context.Background(), "native"); err == nil {
			l.Finish(true)
			t.Fatal("unsafe grant")
		}
	}
	m.set(model.Activity{Available: true, IdleMillis: 120000})
	if err := e.Pause(true); err != nil {
		t.Fatal(err)
	}
	if _, err := e.AcquireDelegation(context.Background(), "native"); err == nil {
		t.Fatal("paused granted")
	}
}

func TestUnconfirmedDelegationSurvivesRestart(t *testing.T) {
	e, d, m, cfg := setup(t)
	m.set(model.Activity{Available: true, IdleMillis: 120000, Generation: 1})
	lease, err := e.AcquireDelegation(context.Background(), "native-restart")
	if err != nil {
		t.Fatal(err)
	}
	if err = lease.Finish(false); err != nil {
		t.Fatal(err)
	}
	if err = e.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(cfg, d, m)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err = reopened.Apps(context.Background()); err == nil || err.Error() != "external_execution_unresolved" {
		t.Fatalf("restart lost unresolved guard: %v", err)
	}
	if err = reopened.ConfirmDelegationStopped("native-restart"); err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.Apps(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestWaitDelegationUsesContinuousIdleAndNeverSendsInput(t *testing.T) {
	e, d, m, _ := setup(t)
	m.set(model.Activity{Available: true, IdleMillis: 0, Generation: 1})
	waiting := make(chan struct{}, 1)
	result := make(chan *DelegationLease, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		l, _ := e.WaitAcquireDelegation(ctx, "waiting-native", func() { waiting <- struct{}{} })
		result <- l
	}()
	select {
	case <-waiting:
	case <-time.After(time.Second):
		t.Fatal("no waiting state")
	}
	m.set(model.Activity{Available: true, IdleMillis: 900, Generation: 1})
	time.Sleep(30 * time.Millisecond)
	m.set(model.Activity{Available: true, IdleMillis: 0, Generation: 2})
	time.Sleep(30 * time.Millisecond)
	select {
	case <-result:
		t.Fatal("acquired before continuous idle threshold")
	default:
	}
	if d.calls.Load() != 0 {
		t.Fatal("input sent while waiting")
	}
	m.set(model.Activity{Available: true, IdleMillis: 1000, Generation: 2})
	select {
	case l := <-result:
		if l == nil {
			t.Fatal("no lease")
		}
		l.Finish(true)
	case <-time.After(time.Second):
		t.Fatal("never acquired idle lease")
	}
}

func TestWaitDelegationStopsOnCancelPauseLockOrUnavailable(t *testing.T) {
	for _, mode := range []string{"cancel", "pause", "lock", "unavailable"} {
		t.Run(mode, func(t *testing.T) {
			e, _, m, _ := setup(t)
			m.set(model.Activity{Available: true})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			waiting, done := make(chan struct{}, 1), make(chan error, 1)
			go func() {
				_, err := e.WaitAcquireDelegation(ctx, "waiting-native", func() { waiting <- struct{}{} })
				done <- err
			}()
			<-waiting
			switch mode {
			case "cancel":
				cancel()
			case "pause":
				e.Pause(true)
			case "lock":
				m.set(model.Activity{Available: true, Locked: true})
			case "unavailable":
				m.set(model.Activity{})
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("unsafe grant")
				}
			case <-time.After(time.Second):
				t.Fatal("wait failed to stop")
			}
		})
	}
}

func TestWaitDelegationRechecksAfterSoleWriterBecomesAvailable(t *testing.T) {
	e, _, m, _ := setup(t)
	if err := e.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.set(model.Activity{Available: true, IdleMillis: 120000})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := e.WaitAcquireDelegation(ctx, "waiting-native", nil); done <- err }()
	time.Sleep(30 * time.Millisecond)
	m.set(model.Activity{Available: true, Locked: true, IdleMillis: 120000})
	e.unlock()
	select {
	case err := <-done:
		if err == nil || err.Error() != "device_locked" {
			t.Fatalf("missed recheck: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("wait failed to stop")
	}
}

func TestDelegationCancellationWinsWhileWaitingForWriter(t *testing.T) {
	e, _, m, _ := setup(t)
	m.set(model.Activity{Available: true, IdleMillis: 120000})
	if err := e.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		lease, err := e.WaitAcquireDelegation(ctx, "cancelled-waiter", nil)
		if lease != nil {
			lease.Finish(true)
		}
		done <- err
	}()
	cancel()
	e.unlock()
	if err := <-done; err == nil || err.Error() != "cancelled" {
		t.Fatalf("cancelled writer obtained lease: %v", err)
	}
}

func TestDelegationCancellationDuringDurableGrantReturnsCancelledLease(t *testing.T) {
	e, _, m, _ := setup(t)
	m.set(model.Activity{Available: true, IdleMillis: 120000})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.mu.Lock()
	previousSync := e.syncFile
	e.syncFile = func(f *os.File) error { cancel(); return f.Sync() }
	e.mu.Unlock()
	lease, err := e.WaitAcquireDelegation(ctx, "cancelled-grant", nil)
	e.mu.Lock()
	e.syncFile = previousSync
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if lease.Context.Err() == nil {
		lease.Finish(true)
		t.Fatal("stop during durable grant was lost")
	}
	// A durable start cannot be silently dropped: the cancelled lease must be
	// explicitly finished so its stop record clears the external writer guard.
	if err = lease.Finish(true); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Apps(context.Background()); err != nil {
		t.Fatalf("cancelled grant stranded the guard: %v", err)
	}
}
