package engine

import (
	"context"
	"github.com/dotcontrol/dotcontrol/internal/model"
	"sync"
	"time"
)

// DelegationLease gives the official executor the very same sole-writer gate
// as Cua. A cancelled context requests interruption; it is not a stop receipt.
type DelegationLease struct {
	Context context.Context
	finish  func(bool) error
}

func (l *DelegationLease) Finish(stopped bool) error { return l.finish(stopped) }

// WaitAcquireDelegation waits without sending desktop input or reserving the
// sole-writer gate while a human is active. Monitor idle time is the continuous
// interval since actual input; a generation change cannot accumulate old idle.
// AcquireDelegation repeats all checks after acquiring the gate.
func (e *Engine) WaitAcquireDelegation(ctx context.Context, jobID string, waiting func()) (*DelegationLease, error) {
	if !validID.MatchString(jobID) {
		return nil, fail("invalid_id")
	}
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	reported := false
	for {
		if ctx.Err() != nil {
			return nil, fail("cancelled")
		}
		e.mu.Lock()
		err := e.healthyLocked()
		a := e.monitor.Snapshot()
		if err == nil {
			switch {
			case e.paused:
				err = fail("device_paused")
			case !a.Available:
				err = fail("activity_unavailable")
			case a.Locked:
				err = fail("device_locked")
			case e.externalPending != "":
				err = fail("external_execution_unresolved")
			}
		}
		idle := a.IdleMillis >= e.cfg.IdleThreshold.Milliseconds()
		e.mu.Unlock()
		if err != nil {
			return nil, err
		}
		if idle {
			lease, err := e.AcquireDelegation(ctx, jobID)
			if err == nil || err.Error() != "waiting_for_idle" {
				return lease, err
			}
		}
		if !reported && waiting != nil {
			waiting()
			reported = true
		}
		select {
		case <-ctx.Done():
			return nil, fail("cancelled")
		case <-e.stop:
			return nil, fail("closed")
		case <-tick.C:
		}
	}
}

func (e *Engine) AcquireDelegation(ctx context.Context, jobID string) (*DelegationLease, error) {
	if !validID.MatchString(jobID) {
		return nil, fail("invalid_id")
	}
	if err := e.acquire(ctx); err != nil {
		return nil, err
	}
	e.mu.Lock()
	initial := e.monitor.Snapshot()
	err := e.healthyLocked()
	if err == nil {
		switch {
		case ctx.Err() != nil:
			err = fail("cancelled")
		case e.paused:
			err = fail("device_paused")
		case !initial.Available:
			err = fail("activity_unavailable")
		case initial.Locked:
			err = fail("device_locked")
		case initial.IdleMillis < e.cfg.IdleThreshold.Milliseconds():
			err = fail("waiting_for_idle")
		}
	}
	if err == nil {
		err = e.appendEvent(ledgerEvent{Type: "delegation_start", TaskID: jobID})
	}
	if err != nil {
		e.mu.Unlock()
		e.unlock()
		return nil, err
	}
	run, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	e.externalPending = jobID
	e.activeTask, e.activeCancel, e.activeDone = jobID, cancel, done
	e.observations = map[string]model.Observation{}
	e.latest = map[model.Target]string{}
	e.observationGeneration++
	e.mu.Unlock()
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-run.Done():
				return
			case <-tick.C:
				now := e.monitor.Snapshot()
				if !now.Available || now.Locked || now.Generation != initial.Generation {
					cancel()
					return
				}
			}
		}
	}()
	var once sync.Once
	var finishErr error
	return &DelegationLease{Context: run, finish: func(stopped bool) error {
		once.Do(func() {
			cancel()
			<-monitorDone
			e.mu.Lock()
			if stopped {
				finishErr = e.appendEvent(ledgerEvent{Type: "delegation_stopped", TaskID: jobID})
				if finishErr == nil {
					e.externalPending = ""
				}
			}
			e.activeTask, e.activeCancel, e.activeDone = "", nil, nil
			e.mu.Unlock()
			close(done)
			// When unconfirmed, durable externalPending prevents every subsequent
			// desktop call, including after restart, without deadlocking shutdown.
			e.unlock()
		})
		return finishErr
	}}, nil
}

// ConfirmDelegationStopped is internal-only. Call only after reading an owned
// official turn's terminal state, never from a model-provided boolean or UI flag.
func (e *Engine) ConfirmDelegationStopped(jobID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.healthyLocked(); err != nil {
		return err
	}
	if e.externalPending != jobID || jobID == "" {
		return fail("delegation_not_pending")
	}
	if e.activeDone != nil {
		return fail("delegation_still_active")
	}
	if err := e.appendEvent(ledgerEvent{Type: "delegation_stopped", TaskID: jobID}); err != nil {
		return err
	}
	e.externalPending = ""
	return nil
}
