package dotbridge

import (
	"context"
	"encoding/json"
	"github.com/dotcontrol/dotcontrol/internal/dotevents"
	"strings"
	"testing"
	"time"
)

func TestPermanentProofStatusAndRestart(t *testing.T) {
	s, _, id := openTestBridge(t)
	p := testEventsParams(id)
	p.TTL = json.RawMessage(`null`)
	if _, err := s.EventsSubscribe(context.Background(), id, p); err != nil {
		t.Fatal(err)
	}
	status, _ := s.StatusForClient(id)
	if status.Ready || status.Subscription.State != "active" || status.Subscription.ExpiresAt != nil || status.Subscription.RefreshBefore != nil || status.Subscription.RenewalState != "current" {
		t.Fatalf("unproven permanent status: %#v", status)
	}
	proveSustainable(t, s, id)
	s.mu.Lock()
	policy := s.state.Clients[id]
	policy.ProbeVerified = time.Now().Add(-24 * time.Hour)
	s.state.Clients[id] = policy
	err := s.saveState()
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), Config{Root: s.root, Listen: s.listen, Vault: s.vault, MCPQueue: s.mcpQueue})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	status, err = reopened.StatusForClient(id)
	if err != nil || !status.Ready || status.ExpiresAt != nil {
		t.Fatalf("reopened permanent proof: %#v %v", status, err)
	}
	b, _ := json.Marshal(status)
	if strings.Contains(string(b), "0001-01-01") {
		t.Fatalf("zero date: %s", b)
	}
	if err = reopened.EventsUnsubscribe(context.Background(), id, p); err != nil {
		t.Fatal(err)
	}
	status, _ = reopened.StatusForClient(id)
	if status.Ready || status.Subscription.State == "active" {
		t.Fatal("unsubscribe resurrected permanent lease")
	}
}

func TestPermanentInvalidationRequiresNewProof(t *testing.T) {
	for _, action := range []string{"auth", "secret", "revoke", "disable", "unsubscribe"} {
		t.Run(action, func(t *testing.T) {
			s, _, id := openTestBridge(t)
			p := testEventsParams(id)
			p.TTL = json.RawMessage(`null`)
			if _, err := s.EventsSubscribe(context.Background(), id, p); err != nil {
				t.Fatal(err)
			}
			proveSustainable(t, s, id)
			switch action {
			case "auth":
				s.mu.Lock()
				policy := s.state.Clients[id]
				policy.TokenHash = hashToken("new-auth")
				s.state.Clients[id] = policy
				s.mu.Unlock()
			case "secret":
				p.Delivery.Secret = "whsec_MTIzNDU2Nzg5MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTI="
			case "revoke":
				if err := s.RevokeClient(id); err != nil {
					t.Fatal(err)
				}
			case "disable":
				if err := s.Disable(); err != nil {
					t.Fatal(err)
				}
				if err := s.Enable(); err != nil {
					t.Fatal(err)
				}
			case "unsubscribe":
				if err := s.EventsUnsubscribe(context.Background(), id, p); err != nil {
					t.Fatal(err)
				}
			}
			if action != "revoke" {
				if _, err := s.EventsSubscribe(context.Background(), id, p); err != nil {
					t.Fatal(err)
				}
			}
			status, _ := s.StatusForClient(id)
			if status.Ready {
				t.Fatal("invalidated proof revived")
			}
		})
	}
}

func TestPermanentAndFiniteGrantsIntersect(t *testing.T) {
	now := time.Now()
	finite := now.Add(time.Hour)
	cases := []struct {
		name      string
		bridge    subscriptionProof
		event     dotevents.TrustedBinding
		permanent bool
	}{
		{"both permanent", subscriptionProof{Permanent: true}, dotevents.TrustedBinding{Permanent: true}, true},
		{"bridge finite", subscriptionProof{Expires: finite}, dotevents.TrustedBinding{Permanent: true}, false},
		{"event finite", subscriptionProof{Permanent: true}, dotevents.TrustedBinding{Expires: finite}, false},
		{"revoked bridge", subscriptionProof{}, dotevents.TrustedBinding{Permanent: true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			grant := intersectLease(tc.bridge, tc.event)
			if grant.Permanent != tc.permanent {
				t.Fatal("permanence changed")
			}
			if tc.name == "revoked bridge" {
				if grant.leaseActive(now) {
					t.Fatal("revoked bridge revived")
				}
				return
			}
			if !grant.leaseActive(now) {
				t.Fatal("active lease lost")
			}
			if !tc.permanent && (!grant.Expires.Equal(finite) || grant.leaseActive(finite)) {
				t.Fatal("finite bound lost")
			}
		})
	}
}
