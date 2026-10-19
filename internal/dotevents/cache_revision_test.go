package dotevents

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestCacheExpiryFailureAndRevocation(t *testing.T) {
	var challenges atomic.Int32
	var fail atomic.Bool
	m, dir, stop, _ := withFixtureServerURL(t, func(w http.ResponseWriter, r *http.Request) {
		challenges.Add(1)
		if fail.Load() {
			w.WriteHeader(503)
			return
		}
		echoVerification(w, r)
	})
	defer stop()
	p := testParams("alice")
	first, e := m.Subscribe(context.Background(), "alice", p)
	if e != nil {
		t.Fatal(e)
	}
	reopened, e := Open(dir, testKey, m.delivery)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = reopened.Subscribe(context.Background(), "alice", p); e != nil {
		t.Fatal(e)
	}
	if challenges.Load() != 1 {
		t.Fatal("cache not persisted")
	}
	_ = m.store.withLock(false, true, func(st *persistenceState) error {
		for _, s := range st.Subscriptions {
			s.VerifiedUntil = time.Now().Add(-time.Second)
		}
		return nil
	})
	fail.Store(true)
	if _, e = m.Subscribe(context.Background(), "alice", p); e == nil {
		t.Fatal("expired cache bypassed challenge")
	}
	status, _ := m.Status("alice", "alice")
	if !status.CallbackVerified {
		t.Fatal("failed refresh destroyed accepted subscription")
	}
	_ = m.store.withLock(false, false, func(st *persistenceState) error {
		if st.Subscriptions[first.ID].ExpiresAt.After(first.ExpiresAt.Add(time.Second)) {
			t.Error("failed refresh extended TTL")
		}
		return nil
	})
	if e = m.SetEnabled(false); e != nil {
		t.Fatal(e)
	}
	if e = m.SetEnabled(true); e != nil {
		t.Fatal(e)
	}
	if _, e = m.Subscribe(context.Background(), "alice", p); e == nil {
		t.Fatal("pause retained cache")
	}
	status, _ = m.Status("alice", "alice")
	if status.CallbackVerified {
		t.Fatal("failed post-revoke refresh revived callback")
	}
	fail.Store(false)
	if _, e = m.Subscribe(context.Background(), "alice", p); e != nil {
		t.Fatal(e)
	}
	if e = m.Unsubscribe(context.Background(), "alice", p); e != nil {
		t.Fatal(e)
	}
	before := challenges.Load()
	if _, e = m.Subscribe(context.Background(), "alice", p); e != nil {
		t.Fatal(e)
	}
	if challenges.Load() != before+1 {
		t.Fatal("unsubscribe retained cache")
	}
}
func TestFixedOccurrenceDelayedReopened(t *testing.T) {
	original := time.Date(2020, 2, 3, 4, 5, 6, 123456789, time.FixedZone("offset", 3600))
	var sends atomic.Int32
	m, dir, stop, _ := withFixtureServerURL(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var v map[string]json.RawMessage
		_ = json.Unmarshal(b, &v)
		if string(v["type"]) == `"verification"` {
			var c string
			_ = json.Unmarshal(v["challenge"], &c)
			_ = json.NewEncoder(w).Encode(map[string]string{"challenge": c})
			return
		}
		var stamp string
		_ = json.Unmarshal(v["timestamp"], &stamp)
		if stamp != original.Format(time.RFC3339Nano) {
			t.Errorf("occurrence timestamp %s", stamp)
		}
		if r.Header.Get("webhook-id") != "evt.delayed" {
			t.Error("source identity changed")
		}
		sends.Add(1)
		w.WriteHeader(500)
	})
	defer stop()
	if _, e := m.Subscribe(context.Background(), "alice", testParams("alice")); e != nil {
		t.Fatal(e)
	}
	reopened, e := Open(dir, testKey, m.delivery)
	if e != nil {
		t.Fatal(e)
	}
	n := Notice{EventID: "evt.delayed", QueueID: "alice", JobID: "job.delayed", Mode: "answer", OccurredAt: original}
	_ = reopened.Deliver(context.Background(), "alice", "alice", n)
	_ = m.store.withLock(false, true, func(st *persistenceState) error {
		for _, r := range st.Deliveries {
			r.NextAttempt = time.Time{}
		}
		return nil
	})
	n.OccurredAt = time.Now()
	_ = m.Deliver(context.Background(), "alice", "alice", n)
	if sends.Load() != 2 {
		t.Fatal("missing attempts")
	}
}
