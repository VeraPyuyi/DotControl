package dotbridge

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/dotevents"
	"github.com/dotcontrol/dotcontrol/internal/dotqueue"
)

type afterQueue struct {
	QueueAPI
	after func()
}

func (q afterQueue) ByRequest(a, b string) (dotqueue.Job, error) {
	j, e := q.QueueAPI.ByRequest(a, b)
	q.after()
	return j, e
}
func (q afterQueue) Cancel(a, b string) (dotqueue.Job, error) {
	j, e := q.QueueAPI.Cancel(a, b)
	q.after()
	return j, e
}
func (q afterQueue) Claim(a, b, c string) (dotqueue.Claim, error) {
	j, e := q.QueueAPI.Claim(a, b, c)
	q.after()
	return j, e
}
func (q afterQueue) Progress(a, b, c, d string) (dotqueue.Job, error) {
	j, e := q.QueueAPI.Progress(a, b, c, d)
	q.after()
	return j, e
}
func (q afterQueue) Complete(a, b string, r dotqueue.Result) (dotqueue.Job, error) {
	j, e := q.QueueAPI.Complete(a, b, r)
	q.after()
	return j, e
}

type afterEvents struct {
	EventsAPI
	after func()
}

func (e afterEvents) Subscribe(ctx context.Context, p string, params dotevents.Params) (dotevents.Subscription, error) {
	s, err := e.EventsAPI.Subscribe(ctx, p, params)
	e.after()
	return s, err
}
func (e afterEvents) Unsubscribe(ctx context.Context, p string, params dotevents.Params) error {
	err := e.EventsAPI.Unsubscribe(ctx, p, params)
	e.after()
	return err
}

func TestPostIORevocationGates(t *testing.T) {
	for _, op := range []string{"request", "cancel", "claim", "progress", "complete", "subscribe", "unsubscribe"} {
		t.Run(op, func(t *testing.T) {
			s, _, id := openTestBridge(t)
			probe, e := s.StartProbe(id)
			if e != nil {
				t.Fatal(e)
			}
			claim, e := s.ClaimTask(id, probe.JobID, "retry")
			if e != nil {
				t.Fatal(e)
			}
			revoke := func() {
				if e := s.RevokeClient(id); e != nil {
					t.Fatal(e)
				}
			}
			s.queue = afterQueue{QueueAPI: s.queue, after: revoke}
			s.events = afterEvents{EventsAPI: s.events, after: revoke}
			switch op {
			case "request":
				_, e = s.byRequest(ClientPolicy{ClientID: id}, probe.RequestID)
			case "cancel":
				_, e = s.cancelJob(ClientPolicy{ClientID: id}, probe.JobID)
			case "claim":
				_, e = s.ClaimTask(id, probe.JobID, "retry")
			case "progress":
				_, e = s.ProgressTask(probe.JobID, claim.Token, dotqueue.StateRunning, "running")
			case "complete":
				_, e = s.CompleteTask(probe.JobID, claim.Token, dotqueue.Result{Text: "reported"})
			case "subscribe":
				_, e = s.EventsSubscribe(context.Background(), id, testEventsParams(id))
			case "unsubscribe":
				e = s.EventsUnsubscribe(context.Background(), id, testEventsParams(id))
			}
			if e == nil {
				t.Fatal("success after concurrent revocation")
			}
		})
	}
}
func TestReadinessSubscriptionGenerationAndExpiry(t *testing.T) {
	s, _, id := openTestBridge(t)
	p, e := s.StartProbe(id)
	if e != nil {
		t.Fatal(e)
	}
	c, e := s.ClaimTask(id, p.JobID, "attempt")
	if e != nil {
		t.Fatal(e)
	}
	answer := c.Submission.Messages[0].Content[len("Reply with exactly this nonce: "):]
	// Renewing a subscription invalidates claims made under its previous generation.
	VerifyTestSubscription(t, s, id)
	if _, e = s.ClaimTask(id, p.JobID, "attempt"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.CompleteTask(p.JobID, c.Token, dotqueue.Result{Text: answer}); e != nil {
		t.Fatal(e)
	}
	status, _ := s.StatusForClient(id)
	if status.Ready {
		t.Fatal("old subscription claim promoted readiness")
	}
	p, e = s.StartProbe(id)
	if e != nil {
		t.Fatal(e)
	}
	c, e = s.ClaimTask(id, p.JobID, "attempt2")
	if e != nil {
		t.Fatal(e)
	}
	answer = c.Submission.Messages[0].Content[len("Reply with exactly this nonce: "):]
	s.mu.Lock()
	sub := s.subscriptions[id]
	sub.Expires = time.Now().Add(time.Minute)
	s.subscriptions[id] = sub
	s.mu.Unlock()
	if _, e = s.CompleteTask(p.JobID, c.Token, dotqueue.Result{Text: answer}); e != nil {
		t.Fatal(e)
	}
	status, _ = s.StatusForClient(id)
	if !status.Ready || status.ExpiresAt.After(sub.Expires) {
		t.Fatalf("expiry: %#v", status)
	}
}
func TestGateEnabledConcurrent(t *testing.T) {
	s, _, _ := openTestBridge(t)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_ = s.gate()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			s.mu.Lock()
			s.state.Enabled = !s.state.Enabled
			s.mu.Unlock()
		}
	}()
	wg.Wait()
}
func TestForeignQueueAndClaimRefused(t *testing.T) {
	s, _, id := openTestBridge(t)
	p, e := s.StartProbe(id)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ClaimTask("foreign", p.JobID, "attempt"); e == nil {
		t.Fatal("foreign queue")
	}
	if _, e = s.TaskStatus(id, p.JobID, "forged"); e == nil {
		t.Fatal("forged token")
	}
	if e = s.EventsUnsubscribe(context.Background(), id, testEventsParams("foreign")); e == nil {
		t.Fatal("foreign events")
	}
}

type brokenVault struct{ writes int }

func (v *brokenVault) Get(string) (string, error) { return "", errors.New("vault unavailable") }
func (v *brokenVault) Set(string, string) error   { v.writes++; return nil }
func TestUnavailableVaultNotOverwritten(t *testing.T) {
	v := &brokenVault{}
	if EnsureStateKey(v) == nil || v.writes != 0 {
		t.Fatal("overwrote unavailable vault")
	}
}
