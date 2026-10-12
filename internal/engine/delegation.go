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
