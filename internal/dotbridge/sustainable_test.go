package dotbridge

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/dotqueue"
)

func proveSustainable(t *testing.T, s *Service, id string) {
	t.Helper()
	p, err := s.StartProbe(id)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.ClaimTask(id, p.JobID, "sustainable")
	if err != nil {
		t.Fatal(err)
	}
	nonce := strings.TrimPrefix(c.Submission.Messages[0].Content, "Reply with exactly this nonce: ")
	if _, err = s.CompleteTask(p.JobID, c.Token, dotqueue.Result{Text: nonce}); err != nil {
		t.Fatal(err)
	}
}

func TestSustainableRenewalAndRestart(t *testing.T) {
	s, _, id := openTestBridge(t)
	proveSustainable(t, s, id)
	s.mu.Lock()
	p := s.state.Clients[id]
	proof := p.proof(dotqueue.ModeAnswer)
	// A mode proven more than ten minutes ago remains valid through its grant.
	proof.Verified = time.Now().Add(-11 * time.Minute)
	p.setProof(dotqueue.ModeAnswer, proof)
	s.state.Clients[id] = p
	s.mu.Unlock()
	VerifyTestSubscription(t, s, id)
	status, _ := s.StatusForClient(id)
	if !status.Ready {
		t.Fatalf("continuous renewal lost proof: %#v", status)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), Config{Root: s.root, Listen: s.listen, Vault: s.vault, MCPQueue: s.mcpQueue})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	status, _ = reopened.StatusForClient(id)
	if !status.Ready {
		t.Fatalf("restart lost durable trust: %#v", status)
	}
}

func TestSustainableBindingChangesInvalidate(t *testing.T) {
	for _, change := range []string{"callback", "secret", "unsubscribe", "auth"} {
		t.Run(change, func(t *testing.T) {
			s, _, id := openTestBridge(t)
			proveSustainable(t, s, id)
			p := testEventsParams(id)
			switch change {
			case "auth":
				s.mu.Lock(); policy := s.state.Clients[id]; policy.TokenHash = hashToken("replacement-credential"); s.state.Clients[id] = policy; s.mu.Unlock()
			case "callback":
				p.Delivery.URL += "&changed=1"
			case "secret":
				p.Delivery.Secret = "whsec_MTIzNDU2Nzg5MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTI="
			case "unsubscribe":
				if err := s.EventsUnsubscribe(context.Background(), id, p); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.EventsSubscribe(context.Background(), id, p); err != nil {
				t.Fatal(err)
			}
			status, _ := s.StatusForClient(id)
			if status.Ready {
				t.Fatal("changed binding retained proof")
			}
		})
	}
}
