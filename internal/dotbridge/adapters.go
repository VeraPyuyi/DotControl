package dotbridge

import (
	"context"

	"github.com/dotcontrol/dotcontrol/internal/dotevents"
	"github.com/dotcontrol/dotcontrol/internal/dotqueue"
)

// QueueAPI isolates Task3 integration from dotqueue store details.
type QueueAPI interface {
	Initialize(enabled bool) error
	SetEnabled(enabled bool) error
	Submit(clientID string, sub dotqueue.Submission) (dotqueue.Job, error)
	Get(clientID, jobID string) (dotqueue.Job, error)
	ByRequest(clientID, requestID string) (dotqueue.Job, error)
	Claim(queueID, jobID, workerID string) (dotqueue.Claim, error)
	Progress(jobID, token, state, summary string) (dotqueue.Job, error)
	Complete(jobID, token string, result dotqueue.Result) (dotqueue.Job, error)
	Cancel(clientID, jobID string) (dotqueue.Job, error)
	Recover() error
	Pending(queueID string) ([]dotqueue.Notice, error)
}

// EventsAPI isolates Task3 integration from dotevents manager details.
type EventsAPI interface {
	SetEnabled(enabled bool) error
	Subscribe(ctx context.Context, principal string, params dotevents.Params) (dotevents.Subscription, error)
	Unsubscribe(ctx context.Context, principal string, params dotevents.Params) error
	List(principal string) []dotevents.Definition
	Deliver(ctx context.Context, principal, queueID string, notice dotevents.Notice) error
	Status(principal, queueID string) (dotevents.Status, error)
}

type queueAdapter struct{ *dotqueue.Store }

type eventsAdapter struct{ *dotevents.Manager }

func wrapQueue(s *dotqueue.Store) QueueAPI      { return queueAdapter{s} }
func wrapEvents(m *dotevents.Manager) EventsAPI { return eventsAdapter{m} }

func (q queueAdapter) Initialize(enabled bool) error { return q.Store.Initialize(enabled) }
func (q queueAdapter) SetEnabled(enabled bool) error { return q.Store.SetEnabled(enabled) }
func (q queueAdapter) Submit(clientID string, sub dotqueue.Submission) (dotqueue.Job, error) {
	return q.Store.Submit(clientID, sub)
}
func (q queueAdapter) Get(clientID, jobID string) (dotqueue.Job, error) {
	return q.Store.Get(clientID, jobID)
}
func (q queueAdapter) ByRequest(clientID, requestID string) (dotqueue.Job, error) {
	return q.Store.ByRequest(clientID, requestID)
}
func (q queueAdapter) Claim(queueID, jobID, workerID string) (dotqueue.Claim, error) {
	return q.Store.Claim(queueID, jobID, workerID)
}
func (q queueAdapter) Progress(jobID, token, state, summary string) (dotqueue.Job, error) {
	return q.Store.Progress(jobID, token, state, summary)
}
func (q queueAdapter) Complete(jobID, token string, result dotqueue.Result) (dotqueue.Job, error) {
	return q.Store.Complete(jobID, token, result)
}
func (q queueAdapter) Cancel(clientID, jobID string) (dotqueue.Job, error) {
	return q.Store.Cancel(clientID, jobID)
}
func (q queueAdapter) Recover() error { return q.Store.Recover() }
func (q queueAdapter) Pending(queueID string) ([]dotqueue.Notice, error) {
	return q.Store.Pending(queueID)
}

func (e eventsAdapter) SetEnabled(enabled bool) error { return e.Manager.SetEnabled(enabled) }
func (e eventsAdapter) Subscribe(ctx context.Context, principal string, params dotevents.Params) (dotevents.Subscription, error) {
	return e.Manager.Subscribe(ctx, principal, params)
}
func (e eventsAdapter) Unsubscribe(ctx context.Context, principal string, params dotevents.Params) error {
	return e.Manager.Unsubscribe(ctx, principal, params)
}
func (e eventsAdapter) List(principal string) []dotevents.Definition {
	return e.Manager.List(principal)
}
func (e eventsAdapter) Deliver(ctx context.Context, principal, queueID string, notice dotevents.Notice) error {
	return e.Manager.Deliver(ctx, principal, queueID, notice)
}
func (e eventsAdapter) Status(principal, queueID string) (dotevents.Status, error) {
	return e.Manager.Status(principal, queueID)
}
