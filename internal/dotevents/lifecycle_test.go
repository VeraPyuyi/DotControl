package dotevents

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func echoVerification(w http.ResponseWriter, r *http.Request) bool {
	var v map[string]json.RawMessage
	b, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(b, &v)
	if string(v["type"]) == `"verification"` {
		var c string
		_ = json.Unmarshal(v["challenge"], &c)
		_ = json.NewEncoder(w).Encode(map[string]string{"challenge": c})
		return true
	}
	return false
}
func TestVerificationGenerationAndPause(t *testing.T) {
	for _, action := range []string{"refresh", "unsubscribe", "pause"} {
		t.Run(action, func(t *testing.T) {
			entered := make(chan struct{})
			release := make(chan struct{})
			var calls atomic.Int32
			m, _, close, _ := withFixtureServerURL(t, func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					closeChannel(entered)
					<-release
				}
				echoVerification(w, r)
			})
			defer close()
			p := testParams("alice")
			done := make(chan error, 1)
			go func() { _, e := m.Subscribe(context.Background(), "alice", p); done <- e }()
			<-entered
			switch action {
			case "refresh":
				p.Delivery.Secret = "whsec_" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("x"), 32))
				if _, e := m.Subscribe(context.Background(), "alice", p); e != nil {
					t.Fatal(e)
				}
			case "unsubscribe":
				p.Delivery.Secret = ""
				if e := m.Unsubscribe(context.Background(), "alice", p); e != nil {
					t.Fatal(e)
				}
			case "pause":
				if e := m.SetEnabled(false); e != nil {
					t.Fatal(e)
				}
				if e := m.SetEnabled(true); e != nil {
					t.Fatal(e)
				}
			}
			closeChannel(release)
			if e := <-done; e == nil {
				t.Fatal("old verification accepted")
			}
			s, _ := m.Status("alice", "alice")
			if action != "refresh" && s.CallbackVerified {
				t.Fatal("revoked verification revived")
			}
		})
	}
}
func closeChannel(c chan struct{}) { close(c) }
func TestRetriesPersistOneAttemptAndTerminal(t *testing.T) {
	for _, status := range []int{500, 429, 410, 413, 400, 302} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var sends atomic.Int32
			var ids []string
			var bodies [][]byte
			m, dir, close, _ := withFixtureServerURL(t, func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				var v map[string]json.RawMessage
				_ = json.Unmarshal(b, &v)
				if string(v["type"]) == `"verification"` {
					var c string
					_ = json.Unmarshal(v["challenge"], &c)
					_ = json.NewEncoder(w).Encode(map[string]string{"challenge": c})
					return
				}
				sends.Add(1)
				ids = append(ids, r.Header.Get("webhook-id"))
				bodies = append(bodies, b)
				w.WriteHeader(status)
			})
			defer close()
			if _, e := m.Subscribe(context.Background(), "alice", testParams("alice")); e != nil {
				t.Fatal(e)
			}
			n := Notice{EventID: "evt.retry", QueueID: "alice", JobID: "job.retry", Mode: "answer", OccurredAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
			e := m.Deliver(context.Background(), "alice", "alice", n)
			if e == nil || sends.Load() != 1 {
				t.Fatalf("attempt %v %d", e, sends.Load())
			}
			s, _ := m.Status("alice", "alice")
			if s.Attempts != 1 {
				t.Fatal("failure attempt not persisted")
			}
			retry := status == 500 || status == 429
			if retry && asEventsError(e).RetryAfter != time.Second {
				t.Fatalf("retry %v", e)
			}
			if !retry && asEventsError(e).RetryAfter != 0 {
				t.Fatal("terminal retry")
			}
			reopened, e := Open(dir, testKey, m.delivery)
			if e != nil {
				t.Fatal(e)
			}
			for attempt := 2; attempt <= 5; attempt++ {
				if retry {
					_ = m.store.withLock(false, true, func(st *persistenceState) error {
						for _, r := range st.Deliveries {
							r.NextAttempt = time.Time{}
						}
						return nil
					})
				}
				_ = reopened.Deliver(context.Background(), "alice", "alice", n)
			}
			want := int32(1)
			if retry {
				want = 4
			}
			if sends.Load() != want {
				t.Fatalf("unbounded sends %d", sends.Load())
			}
			for i := range ids {
				if ids[i] != n.EventID || !bytes.Equal(bodies[0], bodies[i]) {
					t.Fatal("retry changed identity or body")
				}
			}
		})
	}
}
func TestUnsubscribeExpiryEncryptionAndSignatures(t *testing.T) {
	var checked atomic.Bool
	m, dir, close, _ := withFixtureServerURL(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var v map[string]json.RawMessage
		_ = json.Unmarshal(b, &v)
		key, _ := decodeSecret(testSecret())
		mac := hmac.New(sha256.New, key)
		mac.Write([]byte(r.Header.Get("webhook-id") + "." + r.Header.Get("webhook-timestamp") + "." + string(b)))
		want := "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
		if r.Header.Get("webhook-signature") != want {
			t.Error("signature does not match exact transmitted bytes")
		}
		if string(v["type"]) == `"verification"` {
			var c string
			_ = json.Unmarshal(v["challenge"], &c)
			_ = json.NewEncoder(w).Encode(map[string]string{"challenge": c})
			return
		}
		checked.Store(true)
		w.WriteHeader(204)
	})
	defer close()
	p := testParams("alice")
	p.TTL = json.RawMessage(`null`)
	sub, e := m.Subscribe(context.Background(), "alice", p)
	if e != nil {
		t.Fatal(e)
	}
	if time.Until(sub.ExpiresAt) > time.Hour {
		t.Fatal("unbounded null ttl")
	}
	if e = m.Deliver(context.Background(), "alice", "alice", Notice{EventID: "evt.signed", QueueID: "alice", JobID: "job.signed", Mode: "task", OccurredAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}); e != nil {
		t.Fatal(e)
	}
	if !checked.Load() {
		t.Fatal("no signed event")
	}
	disk, _ := os.ReadFile(filepath.Join(dir, stateFileName))
	if bytes.Contains(disk, []byte(p.Delivery.Secret)) || bytes.Contains(disk, []byte("opaque=ok")) {
		t.Fatal("plaintext persisted")
	}
	_ = m.store.withLock(false, true, func(st *persistenceState) error {
		for _, s := range st.Subscriptions {
			s.ExpiresAt = time.Now().Add(-time.Second)
		}
		return nil
	})
	if e = m.Deliver(context.Background(), "alice", "alice", Notice{EventID: "evt.expired", QueueID: "alice", JobID: "job.signed", Mode: "task", OccurredAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}); e == nil {
		t.Fatal("expired sent")
	}
	p.Delivery.Secret = ""
	for i := 0; i < 2; i++ {
		if e = m.Unsubscribe(context.Background(), "alice", p); e != nil {
			t.Fatal(e)
		}
	}
	wrong, _ := Open(dir, bytes.Repeat([]byte("x"), 32), nil)
	if wrong != nil {
		t.Fatal("wrong key accepted")
	}
}
func TestRedirectNotFollowedAndMismatch(t *testing.T) {
	for _, kind := range []string{"redirect", "mismatch", "raw"} {
		t.Run(kind, func(t *testing.T) {
			m, _, close, _ := withFixtureServerURL(t, func(w http.ResponseWriter, r *http.Request) {
				if kind == "redirect" {
					w.Header().Set("Location", "https://127.0.0.1/")
					w.WriteHeader(302)
					return
				}
				if kind == "raw" {
					_, _ = w.Write([]byte("not-json"))
					return
				}
				_, _ = w.Write([]byte(`{"challenge":"wrong"}`))
			})
			defer close()
			_, e := m.Subscribe(context.Background(), "alice", testParams("alice"))
			if e == nil || strings.Contains(e.Error(), "opaque") {
				t.Fatalf("verification error %v", e)
			}
		})
	}
}
