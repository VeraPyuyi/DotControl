package dotbridge

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/dotevents"
)

type subscriptionProof struct {
	BindingID  string
	TrustEpoch string
	AuthHash   string
	Consumer   string
	Refreshed  time.Time
	Expires    time.Time
	Generation uint64
}
type claimBinding struct {
	QueueID, Token string
	Generation     uint64
}

// The runtime's authenticated single consumer may act only on locally paired queues.
func (s *Service) authorizeQueue(queue string) error {
	if e := s.gate(); e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mcpQueue == "" {
		return errors.New("mcp_consumer_unconfigured")
	}
	p, ok := s.state.Clients[queue]
	if !ok || p.Revoked || p.QueueID != queue {
		return errors.New("queue_unauthorized")
	}
	return nil
}
func (s *Service) clientGate(id string) error { return s.authorizeQueue(id) }
func (s *Service) binding(job, token string) (claimBinding, error) {
	s.mu.Lock()
	b, ok := s.claims[job]
	s.mu.Unlock()
	if !ok || !tokenMatches(hashToken(b.Token), token) {
		return b, errors.New("claim_unauthorized")
	}
	return b, s.authorizeQueue(b.QueueID)
}
func (s *Service) activeSubscription(queue string) (subscriptionProof, bool) {
	s.mu.Lock()
	sub, ok := s.subscriptions[queue]
	policy := s.state.Clients[queue]
	consumer := s.mcpQueue
	s.mu.Unlock()
	if sub.TrustEpoch != "" {
		if sub.AuthHash != policy.TokenHash || sub.Consumer != consumer {
			return sub, false
		}
		if reader, supported := s.events.(interface {
			TrustedBindings(string) ([]dotevents.TrustedBinding, error)
		}); supported {
			bindings, err := reader.TrustedBindings(queue)
			if err != nil {
				return sub, false
			}
			found := false
			for _, b := range bindings {
				if b.ID == sub.BindingID && b.Epoch == sub.TrustEpoch {
					found = true
					if b.Expires.Before(sub.Expires) {
						sub.Expires = b.Expires
					}
				}
			}
			if !found {
				return sub, false
			}
		}
	}
	return sub, ok && time.Now().Before(sub.Expires)
}
func consumerWorker(consumer, worker string) string {
	h := sha256.Sum256([]byte(consumer + "\x00" + worker))
	return "consumer." + hex.EncodeToString(h[:])
}
