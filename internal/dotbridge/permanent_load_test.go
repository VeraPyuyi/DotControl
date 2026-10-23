package dotbridge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/dotqueue"
)

func rewriteBridgeSubscription(t *testing.T, s *Service, id string, change func(*subscriptionProof)) {
	t.Helper()
	data, err := securelyReadBridgeState(s.root)
	if err != nil {
		t.Fatal(err)
	}
	var st PersistedState
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatal(err)
	}
	sub := st.Subscriptions[id]
	change(&sub)
	st.Subscriptions[id] = sub
	if err := securelyWriteBridgeState(s.root, mustMarshalJSON(st)); err != nil {
		t.Fatal(err)
	}
}

func mustMarshalJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func TestPermanentBridgeProofLoadFailsClosed(t *testing.T) {
	cases := []struct {
		name   string
		change func(*subscriptionProof)
	}{
		{"finite expiry", func(s *subscriptionProof) { s.Expires = time.Now().Add(time.Hour) }},
		{"past expiry", func(s *subscriptionProof) { s.Expires = time.Now().Add(-time.Hour) }},
		{"empty trust epoch", func(s *subscriptionProof) { s.TrustEpoch = "" }},
		{"short binding", func(s *subscriptionProof) { s.BindingID = "short" }},
		{"non-hex binding", func(s *subscriptionProof) { s.BindingID = strings.Repeat("z", 64) }},
		{"empty auth hash", func(s *subscriptionProof) { s.AuthHash = "" }},
		{"empty consumer", func(s *subscriptionProof) { s.Consumer = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _, id := openTestBridge(t)
			p := testEventsParams(id)
			p.TTL = json.RawMessage(`null`)
			if _, err := s.EventsSubscribe(context.Background(), id, p); err != nil {
				t.Fatal(err)
			}
			rewriteBridgeSubscription(t, s, id, func(sub *subscriptionProof) {
				if !sub.Permanent {
					t.Fatalf("expected permanent bridge proof: %#v", sub)
				}
				tc.change(sub)
			})
			// Load the encrypted file through a fresh instance so no valid proof
			// retained in the running service can mask the on-disk record.

			fresh := &Service{root: s.root, state: PersistedState{Version: APIVersion, Clients: map[string]ClientPolicy{}}}
			err := fresh.loadState()
			if err == nil {
				t.Fatalf("inconsistent permanent bridge proof loaded: %#v", fresh.subscriptions[id])
			}
			if err.Error() != "bridge_state_corrupt" {
				t.Fatalf("load error = %v", err)
			}
		})
	}
}

func TestLegacyBridgeProofWithinEpochStillLoads(t *testing.T) {
	s, _, id := openTestBridge(t)
	s.mu.Lock()
	old := s.subscriptions[id]
	old.TrustEpoch = ""
	s.subscriptions[id] = old
	err := s.saveState()
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	fresh := &Service{root: s.root, state: PersistedState{Version: APIVersion, Clients: map[string]ClientPolicy{}}}
	if err := fresh.loadState(); err != nil {
		t.Fatalf("legacy bridge proof rejected: %v", err)
	}
}

func TestPermanentBridgeProofLoadPreservesValidGrant(t *testing.T) {
	s, _, id := openTestBridge(t)
	p := testEventsParams(id)
	p.TTL = json.RawMessage(`null`)
	if _, err := s.EventsSubscribe(context.Background(), id, p); err != nil {
		t.Fatal(err)
	}
	proveSustainable(t, s, id)
	// Reproduce the persisted device proof from the pending probe. This isolates
	// permanent bridge/event resume from the fixture's callback host rebinding.
	s.mu.Lock()
	policy := s.state.Clients[id]
	proof := policy.proof(dotqueue.ModeAnswer)
	proof.Verified, proof.Consumed = time.Now().Add(-time.Minute), true
	policy.setProof(dotqueue.ModeAnswer, proof)
	s.state.Clients[id] = policy
	err := s.saveState()
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := Open(context.Background(), Config{Root: s.root, Listen: s.listen, Vault: s.vault, MCPQueue: s.mcpQueue})
	if err != nil {
		t.Fatal(err)
	}
	status, err := restored.StatusForClient(id)
	if err != nil || !status.Ready || status.Subscription.State != "active" || status.Subscription.ExpiresAt != nil || status.Subscription.RefreshBefore != nil {
		restored.Close()
		t.Fatalf("valid permanent grant lost: %#v %v", status, err)
	}
	if err := restored.Close(); err != nil {
		t.Fatal(err)
	}
}
