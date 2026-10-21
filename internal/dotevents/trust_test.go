package dotevents

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestTrustEpochContinuousLeaseAndGap(t *testing.T) {
	m, dir, stop, _ := withFixtureServerURL(t, func(w http.ResponseWriter, r *http.Request) { echoVerification(w, r) })
	defer stop()
	now := time.Now().UTC()
	p := testParams("alice")
	p.TTL = []byte("3600000")
	first, err := m.subscribe(context.Background(), "alice", p, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if first.TrustEpoch == "" {
		t.Fatal("verified grant missing trust epoch")
	}
	reopened, err := Open(dir, testKey, m.delivery)
	if err != nil {
		t.Fatal(err)
	}
	renewed, err := reopened.subscribe(context.Background(), "alice", p, func() time.Time { return now.Add(20 * time.Minute) })
	if err != nil {
		t.Fatal(err)
	}
	if renewed.TrustEpoch != first.TrustEpoch || !renewed.ExpiresAt.After(first.ExpiresAt) {
		t.Fatal("renewal lost durable epoch or failed to extend requested grant")
	}
	gap, err := reopened.subscribe(context.Background(), "alice", p, func() time.Time { return renewed.ExpiresAt.Add(time.Second) })
	if err != nil {
		t.Fatal(err)
	}
	if gap.TrustEpoch == renewed.TrustEpoch {
		t.Fatal("lease gap retained trust")
	}
}

func TestTrustEpochSlowRefreshCrossesOldExpiry(t *testing.T) {
	now := time.Now().UTC()
	m, _, stop, _ := withFixtureServerURL(t, func(w http.ResponseWriter, r *http.Request) { echoVerification(w, r) })
	defer stop()
	p := testParams("alice")
	p.TTL = []byte("60000")
	first, err := m.subscribe(context.Background(), "alice", p, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	// Expire only the callback verification cache to force actual callback IO.
	if err = m.store.withLock(false, true, func(st *persistenceState) error { st.Subscriptions[first.ID].VerifiedUntil = time.Time{}; return nil }); err != nil {
		t.Fatal(err)
	}
	calls := 0
	second, err := m.subscribe(context.Background(), "alice", p, func() time.Time {
		calls++
		if calls == 1 {
			return now.Add(59 * time.Second)
		}
		return now.Add(61 * time.Second)
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.TrustEpoch == second.TrustEpoch {
		t.Fatal("slow verification resurrected expired trust")
	}
}
