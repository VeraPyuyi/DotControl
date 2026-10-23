package dotevents

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// rewritePersistedSubscription changes one decrypted record and seals it again,
// exercising the same encrypted load path as a restart.
func rewritePersistedSubscription(t *testing.T, m *Manager, id string, change func(*subscriptionRecord)) {
	t.Helper()
	if err := m.store.withLock(false, false, func(st *persistenceState) error {
		rec := st.Subscriptions[id]
		if rec == nil {
			t.Fatalf("subscription %s missing", id)
		}
		cp := *rec
		change(&cp)
		st.Subscriptions[id] = &cp
		return m.store.saveState(st)
	}); err != nil {
		t.Fatal(err)
	}
}

func reopenMustFail(t *testing.T, dir string) {
	t.Helper()
	reopened, err := Open(dir, testKey, nil)
	if err == nil {
		status, _ := reopened.Status("alice", "alice")
		t.Fatalf("inconsistent permanent state loaded: active=%v", status.Subscribed)
	}
	if code := asEventsError(err).Code; code != "corrupt" && code != "unknown" {
		t.Fatalf("load error code = %q, want corrupt/unknown: %v", code, err)
	}
}

func TestPermanentRecordLoadRejectsInconsistentPersistence(t *testing.T) {
	finite := json.RawMessage(`3600000`)
	cases := []struct {
		name   string
		ttl    json.RawMessage
		change func(*subscriptionRecord)
	}{
		{"permanent numeric ttl", finite, func(r *subscriptionRecord) { r.ExpiresAt = time.Time{} }},
		{"permanent omitted ttl", nil, func(r *subscriptionRecord) { r.ExpiresAt = time.Time{} }},
		{"permanent future expiry", nil, func(r *subscriptionRecord) { r.ExpiresAt = time.Now().Add(time.Hour) }},
		{"permanent past expiry", nil, func(r *subscriptionRecord) { r.ExpiresAt = time.Now().Add(-time.Hour) }},
		{"finite zero expiry", nil, func(r *subscriptionRecord) { r.Permanent = false; r.ExpiresAt = time.Time{} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, dir, cleanup, _ := withFixtureServerURL(t, func(w http.ResponseWriter, r *http.Request) { echoVerification(w, r) })
			defer cleanup()
			p := testParams("alice")
			p.TTL = json.RawMessage(`null`)
			sub, err := m.Subscribe(context.Background(), "alice", p)
			if err != nil {
				t.Fatal(err)
			}
			rewritePersistedSubscription(t, m, sub.ID, func(r *subscriptionRecord) {
				r.Params.TTL = tc.ttl
				tc.change(r)
			})
			reopenMustFail(t, dir)
		})
	}
}

func TestPermanentRecordLoadPreservesGrantsAndLegacyFinite(t *testing.T) {
	m, dir, cleanup, _ := withFixtureServerURL(t, func(w http.ResponseWriter, r *http.Request) { echoVerification(w, r) })
	defer cleanup()
	p := testParams("alice")
	p.TTL = json.RawMessage(`null`)
	sub, err := m.Subscribe(context.Background(), "alice", p)
	if err != nil {
		t.Fatal(err)
	}
	if reopened, err := Open(dir, testKey, m.delivery); err != nil {
		t.Fatal(err)
	} else if status, _ := reopened.Status("alice", "alice"); !status.Subscribed || !status.Bindings[0].Permanent || !status.Bindings[0].Expires.IsZero() {
		t.Fatalf("valid permanent grant lost: %#v", status)
	}
	// A nullable legacy record predating the permanent flag stays finite.
	rewritePersistedSubscription(t, m, sub.ID, func(r *subscriptionRecord) {
		r.Permanent = false
		r.ExpiresAt = time.Now().Add(time.Hour)
	})
	reopened, err := Open(dir, testKey, m.delivery)
	if err != nil {
		t.Fatal(err)
	}
	status, _ := reopened.Status("alice", "alice")
	if !status.Subscribed || len(status.Bindings) != 1 || status.Bindings[0].Permanent || status.Bindings[0].Expires.IsZero() {
		t.Fatalf("legacy finite lease changed: %#v", status)
	}
	rewritePersistedSubscription(t, reopened, sub.ID, func(r *subscriptionRecord) { r.ExpiresAt = time.Now().Add(-time.Second) })
	expired, err := Open(dir, testKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	status, _ = expired.Status("alice", "alice")
	if status.Subscribed || len(status.Bindings) != 0 {
		t.Fatalf("expired legacy lease active: %#v", status)
	}
}
