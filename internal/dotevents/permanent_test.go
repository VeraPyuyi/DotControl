package dotevents

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestPermanentLeaseLifecycle(t *testing.T) {
	for _, stop := range []string{"unsubscribe", "disable"} {
		t.Run(stop, func(t *testing.T) {
			m, dir, cleanup, _ := withFixtureServerURL(t, func(w http.ResponseWriter, r *http.Request) { echoVerification(w, r) })
			defer cleanup()
			p := testParams("alice")
			p.TTL = json.RawMessage(`null`)
			sub, err := m.subscribe(context.Background(), "alice", p, func() time.Time { return time.Now().Add(-365 * 24 * time.Hour) })
			if err != nil {
				t.Fatal(err)
			}
			if !sub.ExpiresAt.IsZero() {
				t.Fatalf("permanent grant has expiry %v", sub.ExpiresAt)
			}
			reopened, err := Open(dir, testKey, m.delivery)
			if err != nil {
				t.Fatal(err)
			}
			notice := Notice{OccurredAt: time.Now(), EventID: "permanent.event", QueueID: "alice", JobID: "job", Mode: "answer"}
			if err = reopened.Deliver(context.Background(), "alice", "alice", notice); err != nil {
				t.Fatal(err)
			}
			if stop == "unsubscribe" {
				err = reopened.Unsubscribe(context.Background(), "alice", p)
			} else {
				err = reopened.SetEnabled(false)
				if err == nil {
					err = reopened.SetEnabled(true)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			notice.EventID = "stopped.event"
			if err = reopened.Deliver(context.Background(), "alice", "alice", notice); err == nil {
				t.Fatal("invalidated lease delivered")
			}
		})
	}
}

func TestPermanentDowngradeAndFiniteTTL(t *testing.T) {
	m, _, cleanup, _ := withFixtureServerURL(t, func(w http.ResponseWriter, r *http.Request) { echoVerification(w, r) })
	defer cleanup()
	p := testParams("alice")
	p.TTL = json.RawMessage(`null`)
	if sub, err := m.Subscribe(context.Background(), "alice", p); err != nil || !sub.ExpiresAt.IsZero() {
		t.Fatalf("permanent: %#v %v", sub, err)
	}
	for _, raw := range []string{"", "60000", "86400000"} {
		p.TTL = json.RawMessage(raw)
		start := time.Now()
		sub, err := m.Subscribe(context.Background(), "alice", p)
		ttl, _ := parseTTL(p.TTL)
		if err != nil || sub.ExpiresAt.Before(start.Add(time.Duration(ttl)*time.Millisecond)) || sub.ExpiresAt.After(time.Now().Add(time.Duration(ttl)*time.Millisecond)) {
			t.Fatalf("finite %q: %#v %v", raw, sub, err)
		}
	}
	p.TTL = json.RawMessage(`86400001`)
	if _, err := m.Subscribe(context.Background(), "alice", p); err == nil {
		t.Fatal("numeric cap bypassed")
	}
}

func TestPermanentSubscriptionsCountTowardCapacity(t *testing.T) {
	m, _, cleanup, _ := withFixtureServerURL(t, func(w http.ResponseWriter, r *http.Request) { echoVerification(w, r) })
	defer cleanup()
	p := testParams("alice")
	p.TTL = json.RawMessage(`null`)
	if _, err := m.Subscribe(context.Background(), "alice", p); err != nil {
		t.Fatal(err)
	}
	if err := m.store.withLock(false, true, func(st *persistenceState) error {
		var source *subscriptionRecord
		for _, s := range st.Subscriptions {
			source = s
			break
		}
		for i := 1; i < maxActiveSubs; i++ {
			cp := *source
			cp.Params.Delivery.URL += "&slot=" + time.Unix(int64(i), 0).Format("150405")
			cp.ID = subscriptionID("alice", cp.Params)
			st.Subscriptions[cp.ID] = &cp
		}
		if activeSubscriptions(st, time.Now().Add(365*24*time.Hour)) != maxActiveSubs {
			t.Fatal("permanent leases not counted")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	p.Delivery.URL += "&extra=1"
	if _, err := m.Subscribe(context.Background(), "alice", p); err == nil || asEventsError(err).Code != "capacity" {
		t.Fatalf("capacity: %v", err)
	}
}
