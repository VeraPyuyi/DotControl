package dotbridge

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/dotevents"
	"github.com/dotcontrol/dotcontrol/internal/dotqueue"
)

type worker struct {
	svc  *Service
	done chan struct{}
	wg   sync.WaitGroup
}

func newWorker(s *Service) *worker {
	return &worker{svc: s, done: make(chan struct{})}
}

func (w *worker) Start() {
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-w.svc.ctx.Done():
				close(w.done)
				return
			case <-ticker.C:
				w.tick()
			}
		}
	}()
}

func (w *worker) Wait() {
	w.wg.Wait()
}

func (w *worker) tick() {
	w.svc.mu.Lock()
	queues := make([]string, 0, len(w.svc.state.Clients))
	for _, policy := range w.svc.state.Clients {
		if !policy.Revoked {
			queues = append(queues, policy.QueueID)
		}
	}
	enabled := w.svc.state.Enabled
	w.svc.mu.Unlock()
	if !enabled {
		return
	}
	for _, queueID := range queues {
		if err := w.svc.authorizeQueue(queueID); err != nil {
			return
		}
		notices, err := w.svc.queue.Pending(queueID)
		if err != nil {
			continue
		}
		for _, notice := range notices {
			if err := w.svc.authorizeQueue(queueID); err != nil {
				return
			}
			ctx, cancel := context.WithTimeout(w.svc.ctx, 5*time.Second)
			err := w.svc.events.Deliver(ctx, queueID, queueID, dotevents.Notice{
				EventID:    notice.EventID,
				QueueID:    notice.QueueID,
				JobID:      notice.JobID,
				Mode:       notice.Mode,
				OccurredAt: notice.OccurredAt,
			})
			cancel()
			if w.svc.authorizeQueue(queueID) != nil {
				return
			}
			if err != nil {
				continue
			}
		}
	}
}

func (s *Service) claimTask(queueID, jobID, workerID string) (dotqueue.Claim, error) {
	if e := s.authorizeQueue(queueID); e != nil {
		return dotqueue.Claim{}, e
	}
	if workerID == "" {
		return dotqueue.Claim{}, errors.New("worker_required")
	}
	sub, active := s.activeSubscription(queueID)
	if !active {
		return dotqueue.Claim{}, errors.New("subscription_required")
	}
	claim, e := s.queue.Claim(queueID, jobID, consumerWorker(s.mcpQueue, workerID))
	if e != nil {
		return claim, e
	}
	if e = s.authorizeQueue(queueID); e != nil {
		return dotqueue.Claim{}, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Clients[queueID].Revoked {
		return dotqueue.Claim{}, errors.New("access_revoked")
	}
	if _, exists := s.claims[jobID]; !exists {
		s.claims[jobID] = claimBinding{QueueID: queueID, Token: claim.Token, Generation: sub.Generation}
	}
	return claim, nil
}
func (s *Service) progressTask(jobID, token, state, summary string) (dotqueue.Job, error) {
	b, e := s.binding(jobID, token)
	if e != nil {
		return dotqueue.Job{}, e
	}
	j, e := s.queue.Progress(jobID, token, state, summary)
	if post := s.authorizeQueue(b.QueueID); post != nil {
		return dotqueue.Job{}, post
	}
	return j, e
}
func (s *Service) completeTask(jobID, token string, result dotqueue.Result) (dotqueue.Job, error) {
	b, e := s.binding(jobID, token)
	if e != nil {
		return dotqueue.Job{}, e
	}
	job, e := s.queue.Complete(jobID, token, result)
	if e != nil {
		return dotqueue.Job{}, e
	}
	if e = s.authorizeQueue(b.QueueID); e != nil {
		return dotqueue.Job{}, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sub := s.subscriptions[b.QueueID]
	active := time.Now().Before(sub.Expires)
	policy := s.state.Clients[b.QueueID]
	if policy.Revoked {
		return dotqueue.Job{}, errors.New("access_revoked")
	}
	if active && sub.Generation == b.Generation && policy.ProbeJobID == jobID && policy.ProbeAnswer == hashToken(result.Text) && job.State == dotqueue.StateCompleted && job.Mode == dotqueue.ModeAnswer {
		now := time.Now().UTC()
		policy.ProbeVerified = now
		policy.ProbeExpires = now.Add(probeValidity)
		if sub.Expires.Before(policy.ProbeExpires) {
			policy.ProbeExpires = sub.Expires
		}
		policy.Capabilities = CapPolicy{Text: true}
		s.state.Clients[b.QueueID] = policy
		if e := s.saveState(); e != nil {
			return dotqueue.Job{}, e
		}
	}
	return job, nil
}
func (s *Service) taskStatus(principal, jobID, token string) (dotqueue.Job, error) {
	b, e := s.binding(jobID, token)
	if e != nil {
		return dotqueue.Job{}, e
	}
	job, e := s.queue.Get(b.QueueID, jobID)
	if post := s.authorizeQueue(b.QueueID); post != nil {
		return dotqueue.Job{}, post
	}
	return job, e
}
