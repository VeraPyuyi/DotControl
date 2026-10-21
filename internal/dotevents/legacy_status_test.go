package dotevents

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLegacyVerifiedBindingStatusAfterRestart(t *testing.T) {
	m, dir := openTestManager(t)
	now := time.Now().UTC().Truncate(time.Second)
	refreshed, expires := now.Add(-20*time.Minute), now.Add(40*time.Minute-time.Second)
	p := testParams("alice")
	p.TTL = []byte("3600000")
	id := subscriptionID("alice", p)
	if err := m.store.withLock(false, true, func(st *persistenceState) error {
		// Pre-epoch binaries persisted callback verification, but could leave
		// an earlier failed refresh behind after a subsequent success.
		st.Subscriptions[id] = &subscriptionRecord{
			ID: id, Principal: "alice", Params: p, Secret: p.Delivery.Secret,
			Generation: "legacy-generation", CallbackVerified: true,
			CreatedAt: now.Add(-157 * time.Minute), UpdatedAt: refreshed,
			ExpiresAt: expires, VerifiedUntil: refreshed.Add(verifiedCacheTTL),
		}
		st.LastSubscribe = &subscribeFailure{At: now.Add(-65 * time.Minute), Principal: "alice", Reason: "network", Stage: "network"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir, testKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, stateFileName)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	status, err := reopened.Status("alice", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !status.Subscribed || !status.CallbackVerified || len(status.Bindings) != 1 {
		t.Errorf("verified legacy lease omitted: %#v", status)
	} else if b := status.Bindings[0]; b.ID != id || b.Epoch != "" || !b.Expires.Equal(expires) || !b.Refreshed.Equal(refreshed) {
		t.Errorf("legacy binding changed: %#v", b)
	}
	if status.SubscribeFailure != nil {
		t.Errorf("failure predating verified renewal is still current: %#v", status.SubscribeFailure)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("status mutated encrypted state")
	}
}

func TestSubscribeFailureOnlySuppressedByLaterVerifiedRenewal(t *testing.T) {
	for _, tc := range []struct {
		name        string
		principal   string
		verified    bool
		failureAge  time.Duration
		wantFailure bool
	}{
		{"stale", "alice", true, time.Minute, false},
		{"same_time", "alice", true, 0, true},
		{"newer_failure", "alice", true, -time.Minute, true},
		{"unverified", "alice", false, time.Minute, true},
		{"another_queue", "bob", true, time.Minute, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := openTestManager(t)
			now := time.Now().UTC().Truncate(time.Second)
			refreshed := now.Add(-20 * time.Minute)
			p := testParams(tc.principal)
			id := subscriptionID(tc.principal, p)
			if err := m.store.withLock(false, true, func(st *persistenceState) error {
				st.Subscriptions[id] = &subscriptionRecord{
					ID: id, Principal: tc.principal, Params: p, Secret: p.Delivery.Secret,
					Generation: "legacy-generation", CallbackVerified: tc.verified,
					CreatedAt: refreshed, UpdatedAt: refreshed, ExpiresAt: now.Add(40 * time.Minute),
				}
				st.LastSubscribe = &subscribeFailure{At: refreshed.Add(-tc.failureAge), Principal: "alice", Reason: "network", Stage: "network"}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			status, err := m.Status("alice", "alice")
			if err != nil {
				t.Fatal(err)
			}
			if (status.SubscribeFailure != nil) != tc.wantFailure {
				t.Fatalf("current failure: %#v, want present=%v", status.SubscribeFailure, tc.wantFailure)
			}
			if (!tc.verified || tc.principal != "alice") && len(status.Bindings) != 0 {
				t.Fatalf("unverified or foreign binding exposed: %#v", status.Bindings)
			}
		})
	}
}
