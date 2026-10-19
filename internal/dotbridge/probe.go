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
	JobID     string `json:"job_id"`
	RequestID string `json:"request_id"`
	QueueID   string `json:"queue_id"`
	Steps     []string `json:"steps"`
}

func (s *Service) StartProbe(clientID string) (ProbeResult, error) {
	s.mu.Lock()
	policy, ok := s.state.Clients[clientID]
	s.mu.Unlock()
	if !ok || policy.Revoked {
		return ProbeResult{}, errors.New("client_not_found")
	}
	if err := s.gate(); err != nil {
		return ProbeResult{}, err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return ProbeResult{}, err
	}
	answer := hex.EncodeToString(nonce[:])
	requestID := "probe." + time.Now().UTC().Format("20060102150405")
	sub := dotqueue.Submission{
		RequestID: requestID,
		Mode:      dotqueue.ModeAnswer,
		SessionID: "probe",
		Messages: []dotqueue.Message{{
			Role:    "user",
			Content: fmt.Sprintf("Reply with exactly this nonce: %s", answer),
		}},
	}
	job, err := s.queue.Submit(clientID, sub)
	if err != nil {
		return ProbeResult{}, err
	}
	s.mu.Lock()
	policy.ProbeJobID = job.ID
	policy.ProbeNonce = answer
	policy.ProbeAnswer = answer
	policy.ProbeVerified = time.Time{}
	policy.ProbeExpires = time.Time{}
	s.state.Clients[clientID] = policy
	_ = s.saveState()
	s.mu.Unlock()
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
