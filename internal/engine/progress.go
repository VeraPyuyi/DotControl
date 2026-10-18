package engine

import "time"

// ExecutionProgress contains fixed metadata only; native_pending includes cleanup.
// A final unknown receipt does not imply that a native operation has stopped.
type ExecutionProgress struct {
	TaskID           string    `json:"task_id,omitempty"`
	ActionID         string    `json:"action_id,omitempty"`
	State            string    `json:"state,omitempty"`
	Code             string    `json:"code,omitempty"`
	CancelRegistered bool      `json:"cancel_registered"`
	NativePending    bool      `json:"native_pending"`
	CanObserve       bool      `json:"can_observe"`
	QueueMillis      int64     `json:"queue_ms"`
	ExecutionMillis  int64     `json:"execution_ms"`
	SampleTime       time.Time `json:"sample_time"`
}

func (e *Engine) Progress() ExecutionProgress {
	e.mu.Lock()
	defer e.mu.Unlock()
	p := e.progress
	p.SampleTime = time.Now().UTC()
	p.CancelRegistered = e.cancelled[p.TaskID]
	p.CanObserve = !p.NativePending && !e.paused && !e.closed && e.externalPending == ""
	return p
}
