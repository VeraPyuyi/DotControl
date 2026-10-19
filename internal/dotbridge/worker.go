package dotbridge

import (
	"context"
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
		if err := w.svc.gate(); err != nil {
			return
		}
		notices, err := w.svc.queue.Pending(queueID)
		if err != nil {
			continue
		}
		for _, notice := range notices {
			if err := w.svc.gate(); err != nil {
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
			if err != nil {
				continue
			}
		}
	}
}

func (s *Service) claimTask(queueID, jobID, workerID string) (dotqueue.Claim, error) {
	if err := s.gate(); err != nil {
		return dotqueue.Claim{}, err
	}
	authorized, _, err := s.MCPPrincipal()
	if err != nil {
		return dotqueue.Claim{}, err
	}
	if queueID != authorized {
		return dotqueue.Claim{}, &dotqueue.Error{Code: "unknown"}
	}
	return s.queue.Claim(queueID, jobID, workerID)
}

func (s *Service) progressTask(jobID, token, state, summary string) (dotqueue.Job, error) {
	if err := s.gate(); err != nil {
		return dotqueue.Job{}, err
	}
	return s.queue.Progress(jobID, token, state, summary)
}

func (s *Service) completeTask(jobID, token string, result dotqueue.Result) (dotqueue.Job, error) {
	if err := s.gate(); err != nil {
		return dotqueue.Job{}, err
	}
	job, err := s.queue.Complete(jobID, token, result)
	if err != nil {
		return job, err
	}
	s.mu.Lock()
	for id, policy := range s.state.Clients {
		if policy.ProbeJobID == jobID && policy.ProbeAnswer != "" && result.Text == policy.ProbeAnswer {
			now := time.Now().UTC()
			policy.ProbeVerified = now
			policy.ProbeExpires = now.Add(probeValidity)
			policy.Capabilities.Text = true
			s.state.Clients[id] = policy
			_ = s.saveState()
			break
		}
	}
	s.mu.Unlock()
	return job, nil
}

func (s *Service) taskStatus(principal, jobID, token string) (dotqueue.Job, error) {
	if err := s.gate(); err != nil {
		return dotqueue.Job{}, err
	}
	job, err := s.queue.Get(principal, jobID)
	if err != nil {
		return dotqueue.Job{}, err
	}
	return s.queue.Progress(jobID, token, job.State, job.Summary)
}
