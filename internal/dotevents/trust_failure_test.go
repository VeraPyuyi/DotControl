package dotevents

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestFailedRefreshKeepsOldGrantAndReportsFailure(t *testing.T) {
	var fail atomic.Bool
	m, _, stop, _ := withFixtureServerURL(t, func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(500)
			return
		}
		echoVerification(w, r)
	})
	defer stop()
	p := testParams("alice")
	first, err := m.Subscribe(context.Background(), "alice", p)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.store.withLock(false, true, func(st *persistenceState) error { st.Subscriptions[first.ID].VerifiedUntil = time.Time{}; return nil }); err != nil {
		t.Fatal(err)
	}
	fail.Store(true)
	if _, err = m.Subscribe(context.Background(), "alice", p); err == nil {
		t.Fatal("refresh unexpectedly succeeded")
	}
	status, err := m.Status("alice", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !status.Subscribed || !status.CallbackVerified || status.SubscribeFailure == nil {
		t.Fatalf("missing active-lease failure diagnostics: %#v", status)
	}
	if len(status.Bindings) != 1 || status.Bindings[0].Epoch != first.TrustEpoch || !status.Bindings[0].Expires.Equal(first.ExpiresAt) {
		t.Fatal("failed refresh altered authorized grant")
	}
	fail.Store(false)
	if _, err = m.Subscribe(context.Background(), "alice", p); err != nil {
		t.Fatal(err)
	}
	status, err = m.Status("alice", "alice")
	if err != nil || status.SubscribeFailure != nil {
		t.Fatal("successful refresh did not clear failure")
	}
}
