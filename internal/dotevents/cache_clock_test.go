package dotevents

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestCacheClockBounds(t *testing.T) {
	base := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name                        string
		until, oldExpiry, now, want time.Time
		ttl                         string
	}{
		{"backward clock", base.Add(5 * time.Minute), base.Add(time.Hour), base.Add(-time.Hour), base.Add(-55 * time.Minute), "3600000"},
		{"shorter refresh", base.Add(5 * time.Minute), base.Add(time.Hour), base, base.Add(time.Minute), "60000"},
		{"source expiry", base.Add(5 * time.Minute), base.Add(2 * time.Minute), base, base.Add(2 * time.Minute), "3600000"},
		{"no proof extension", base.Add(time.Minute), base.Add(time.Hour), base, base.Add(time.Minute), "3600000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			m, dir, stop, _ := withFixtureServerURL(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); echoVerification(w, r) })
			defer stop()
			p := testParams("alice")
			id := subscriptionID("alice", p)
			if e := m.store.withLock(false, true, func(st *persistenceState) error {
				st.Subscriptions[id] = &subscriptionRecord{ID: id, Generation: "legacy", Principal: "alice", Params: p, Secret: p.Delivery.Secret, CallbackVerified: true, CreatedAt: base, UpdatedAt: base, ExpiresAt: tc.oldExpiry, VerifiedUntil: tc.until}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			p.TTL = []byte(tc.ttl)
			if _, e := m.subscribe(context.Background(), "alice", p, func() time.Time { return tc.now }); e != nil {
				t.Fatal(e)
			}
			reopened, e := Open(dir, testKey, m.delivery)
			if e != nil {
				t.Fatal(e)
			}
			if e = reopened.store.withLock(false, false, func(st *persistenceState) error {
				s := st.Subscriptions[id]
				if !s.VerifiedUntil.Equal(tc.want) {
					t.Errorf("deadline %s, want %s", s.VerifiedUntil, tc.want)
				}
				if !s.CreatedAt.Equal(base) || s.Generation == "legacy" {
					t.Error("refresh lost creation time or generation")
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			if _, e := reopened.subscribe(context.Background(), "alice", p, func() time.Time { return tc.now.Add(30 * time.Second) }); e != nil {
				t.Fatal(e)
			}
			if e := reopened.store.withLock(false, false, func(st *persistenceState) error {
				if !st.Subscriptions[id].VerifiedUntil.Equal(tc.want) {
					t.Error("repeated refresh extended verification proof")
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			if calls.Load() != 0 {
				t.Fatal("cache hit performed IO")
			}
		})
	}
}

func TestSlowVerificationClock(t *testing.T) {
	base := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	for _, elapsed := range []time.Duration{10 * time.Second, 15 * time.Second, 25 * time.Second} {
		t.Run(elapsed.String(), func(t *testing.T) {
			current := base
			m, dir, stop, _ := withFixtureServerURL(t, func(w http.ResponseWriter, r *http.Request) { echoVerification(w, r) })
			defer stop()
			// Model callback plus both lock/save spans without waiting on real time.
			clockCalls := 0
			clock := func() time.Time {
				clockCalls++
				if clockCalls > 1 {
					current = base.Add(elapsed)
				}
				return current
			}
			p := testParams("alice")
			if _, e := m.subscribe(context.Background(), "alice", p, clock); e != nil {
				t.Fatal(e)
			}
			reopened, e := Open(dir, testKey, m.delivery)
			if e != nil {
				t.Fatal(e)
			}
			if e = reopened.store.withLock(false, false, func(st *persistenceState) error {
				s := st.Subscriptions[subscriptionID("alice", p)]
				if !s.VerifiedUntil.Equal(base.Add(elapsed + 5*time.Minute)) {
					t.Errorf("deadline %s", s.VerifiedUntil)
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
		})
	}
}
