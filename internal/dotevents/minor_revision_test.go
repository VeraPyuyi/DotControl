package dotevents

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestMinorFailedVerificationRace(t *testing.T) {
	for _, action := range []string{"pause", "reenable", "unsubscribe", "refresh"} {
		t.Run(action, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			m, _, stop, _ := withFixtureServerURL(t, func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 2 {
					close(entered)
					<-release
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				echoVerification(w, r)
			})
			defer stop()
			p := testParams("alice")
			if _, e := m.Subscribe(context.Background(), "alice", p); e != nil {
				t.Fatal(e)
			}
			if e := m.store.withLock(false, true, func(st *persistenceState) error {
				st.Subscriptions[subscriptionID("alice", p)].VerifiedUntil = time.Now().Add(-time.Second)
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			done := make(chan error, 1)
			go func() { _, e := m.Subscribe(context.Background(), "alice", p); done <- e }()
			<-entered
			var actionErr error
			want := "stale"
			switch action {
			case "pause", "reenable":
				actionErr = m.SetEnabled(false)
				want = "disabled"
				if action == "reenable" && actionErr == nil {
					actionErr = m.SetEnabled(true)
					want = "stale"
				}
			case "unsubscribe":
				actionErr = m.Unsubscribe(context.Background(), "alice", p)
			case "refresh":
				_, actionErr = m.Subscribe(context.Background(), "alice", p)
				want = "verification"
			}
			close(release)
			e := <-done
			if actionErr != nil {
				t.Fatal(actionErr)
			}
			if e == nil || asEventsError(e).Code != want {
				t.Fatalf("error = %v, want %s", e, want)
			}
			status, e := m.Status("alice", "alice")
			if e != nil {
				t.Fatal(e)
			}
			if status.CallbackVerified != (action == "refresh") {
				t.Fatalf("verification after %s: %+v", action, status)
			}
		})
	}
}

func TestMinorVerifiedUntilLoadValidation(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name            string
		until           time.Time
		verified, valid bool
	}{
		{"legacy zero", time.Time{}, true, true},
		{"paused zero", time.Time{}, false, true},
		{"valid cache", now.Add(5 * time.Minute), true, true},
		{"expired cache", now.Add(-time.Minute), true, true},
		{"year zero", time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC), true, false},
		{"excessive lifetime", now.Add(24 * time.Hour), true, false},
		{"unverified cache", now.Add(time.Minute), false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := testParams("alice")
			id := subscriptionID("alice", p)
			st := newPersistenceState()
			st.Subscriptions[id] = &subscriptionRecord{ID: id, Principal: "alice", Params: p, Secret: p.Delivery.Secret, CallbackVerified: tc.verified, VerifiedUntil: tc.until, CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour)}
			data, e := json.Marshal(st)
			if e != nil {
				t.Fatal(e)
			}
			_, e = parsePersistenceState(data)
			if (e == nil) != tc.valid {
				t.Fatalf("load error = %v, valid = %v", e, tc.valid)
			}
		})
	}
}
