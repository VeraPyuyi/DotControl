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
