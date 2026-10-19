package dotbridge

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/dotqueue"
)

type ProbeResult struct {
	JobID     string   `json:"job_id"`
	RequestID string   `json:"request_id"`
	QueueID   string   `json:"queue_id"`
	Steps     []string `json:"steps"`
}

func (s *Service) StartProbe(clientID string) (ProbeResult, error) {
	return s.StartProbeMode(clientID, dotqueue.ModeAnswer)
}

func (s *Service) StartProbeMode(clientID, mode string) (ProbeResult, error) {
	if mode != dotqueue.ModeAnswer && mode != dotqueue.ModeTask {
		return ProbeResult{}, errors.New("invalid_probe_mode")
	}
	s.mu.Lock()
	policy, ok := s.state.Clients[clientID]
	s.mu.Unlock()
	if !ok || policy.Revoked {
		return ProbeResult{}, errors.New("client_not_found")
	}
	if err := s.gate(); err != nil {
		return ProbeResult{}, err
	}
	subscription, active := s.activeSubscription(policy.QueueID)
	if !active {
		return ProbeResult{}, errors.New("subscription_required")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return ProbeResult{}, err
	}
	answer := hex.EncodeToString(nonce[:])
	requestID := "probe." + hashToken(answer)
	sub := dotqueue.Submission{
		RequestID: requestID,
		Mode:      dotqueue.ModeAnswer,
		SessionID: "probe",
		Messages: []dotqueue.Message{{
			Role:    "user",
			Content: fmt.Sprintf("Reply with exactly this nonce: %s", answer),
		}},
	}
	if mode == dotqueue.ModeTask {
		sub.Mode = mode
		sub.Messages = nil
		sub.Task = fmt.Sprintf("In your own cloud workspace, create a temporary file with a unique name, write the nonce into it, read it back, then delete the file. After successful cleanup, return exactly the nonce and nothing else. Nonce: %s", answer)
	}
	job, err := s.queue.Submit(clientID, sub)
	if err != nil {
		return ProbeResult{}, err
	}
	if e := s.clientGate(clientID); e != nil {
		return ProbeResult{}, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	policy, ok = s.state.Clients[clientID]
	if !ok || policy.Revoked {
		return ProbeResult{}, errors.New("access_revoked")
	}
	if s.revoked != nil && s.revoked() {
		return ProbeResult{}, errors.New("access_revoked")
	}
	if s.paused != nil && s.paused() {
		return ProbeResult{}, errors.New("bridge_paused")
	}
	current := s.subscriptions[policy.QueueID]
	if !s.state.Enabled || !time.Now().Before(current.Expires) || current.Generation != subscription.Generation || !current.Expires.Equal(subscription.Expires) {
		return ProbeResult{}, errors.New("subscription_changed")
	}
	policy.setProof(mode, ProbeProof{JobID: job.ID, AnswerHash: hashToken(answer), Generation: subscription.Generation, SubscriptionExpires: subscription.Expires})
	s.state.Clients[clientID] = policy
	if e := s.saveState(); e != nil {
		return ProbeResult{}, e
	}
	return ProbeResult{
		JobID:     job.ID,
		RequestID: requestID,
		QueueID:   policy.QueueID,
		Steps: []string{
			"Connect the private Dot plugin to this DotControl runtime.",
			"Refresh MCP metadata and subscribe to dot.task.available for this queue.",
			"Claim the probe job with dot_task_claim, then complete it with the exact nonce using dot_task_complete.",
		},
	}, nil
}
