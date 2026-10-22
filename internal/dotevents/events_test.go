package dotevents

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestInvalidSubscriptions(t *testing.T) {
	tests := []struct {
		name      string
		principal string
		mutate    func(*Params)
	}{
		{"principal", "bob", func(*Params) {}},
		{"event", "alice", func(p *Params) { p.Name = "other" }},
		{"secret", "alice", func(p *Params) { p.Delivery.Secret = "whsec_bad" }},
		{"ttl", "alice", func(p *Params) { p.TTL = json.RawMessage(`1`) }},
		{"ttl_fraction", "alice", func(p *Params) { p.TTL = json.RawMessage(`60000.5`) }},
		{"cursor", "alice", func(p *Params) { p.Cursor = json.RawMessage(`"resume"`) }},
		{"url", "alice", func(p *Params) { p.Delivery.URL = "http://127.0.0.1" }},
		{"fragment", "alice", func(p *Params) { p.Delivery.URL = "https://callback.example/#x" }},
		{"userinfo", "alice", func(p *Params) { p.Delivery.URL = "https://user@callback.example/" }},
		{"localhost", "alice", func(p *Params) { p.Delivery.URL = "https://localhost/" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr, _ := openTestManager(t)
			p := testParams("alice")
			tt.mutate(&p)
			if _, err := mgr.Subscribe(context.Background(), tt.principal, p); err == nil {
				t.Fatal("accepted invalid subscription")
			}
		})
	}
}

func TestPublicGuard(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.1.1.1", "169.254.169.254", "100.64.1.1", "192.0.2.1", "198.51.100.1", "203.0.113.1", "224.0.0.0", "0.0.0.0", "::1", "fc00::1", "fe80::1", "2001:db8::1", "::ffff:127.0.0.1", "198.18.0.1", "240.0.0.1"} {
		addr, err := netip.ParseAddr(ip)
		if err != nil {
			t.Fatal(err)
		}
		if publicIP(addr) {
			t.Errorf("accepted %s", ip)
		}
	}
}

func TestSubscribeVerifyDeliverAndPersist(t *testing.T) {
	var verifyChallenge string
	var eventBody []byte
	var signatures []string
	mgr, dir, close, _ := withFixtureServerURL(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, maxResponseBytes+1))
		if strings.Contains(string(body), "verification") {
			var payload map[string]string
			_ = json.Unmarshal(body, &payload)
			verifyChallenge = payload["challenge"]
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{"challenge": verifyChallenge})
			return
		}
		eventBody = append([]byte(nil), body...)
		signatures = strings.Fields(r.Header.Get("webhook-signature"))
		w.WriteHeader(http.StatusOK)
	})
	defer close()
	sub, err := mgr.Subscribe(context.Background(), "alice", testParams("alice"))
	if err != nil || !sub.CallbackVerified || verifyChallenge == "" {
		t.Fatalf("subscribe verify failed: %v %#v", err, sub)
	}
	status, err := mgr.Status("alice", "alice")
	if err != nil || !status.Subscribed || !status.CallbackVerified || status.DotReady {
		t.Fatalf("unexpected status %#v", status)
	}
	notice := Notice{OccurredAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), EventID: "evt.1", QueueID: "alice", JobID: "job.1", Mode: "answer"}
	if err := mgr.Deliver(context.Background(), "alice", "alice", notice); err != nil {
		t.Fatal(err)
	}
	var occurrence map[string]json.RawMessage
	if json.Unmarshal(eventBody, &occurrence) != nil || string(occurrence["eventId"]) != `"evt.1"` {
		t.Fatalf("event body mismatch: %q", eventBody)
	}
	if len(signatures) == 0 {
		t.Fatal("missing signature")
	}
	reopened, err := Open(dir, testKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	again, err := reopened.Status("alice", "alice")
	if err != nil || !again.Acknowledged {
		t.Fatalf("persisted ack missing: %#v", again)
	}
}

func TestChallengeMismatchAndOverread(t *testing.T) {
	mgr, _, close, _ := withFixtureServerURL(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, maxResponseBytes+1))
		if strings.Contains(string(body), "verification") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(bytes.Repeat([]byte("x"), maxResponseBytes+1))
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	defer close()
	if _, err := mgr.Subscribe(context.Background(), "alice", testParams("alice")); err == nil {
		t.Fatal("accepted overread challenge")
	}
}

func TestDuplicateEventAck(t *testing.T) {
	mgr, _, close, _ := withFixtureServerURL(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, maxResponseBytes+1))
		if strings.Contains(string(body), "verification") {
			var payload map[string]string
			_ = json.Unmarshal(body, &payload)
			_ = json.NewEncoder(w).Encode(map[string]string{"challenge": payload["challenge"]})
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	defer close()
	if _, err := mgr.Subscribe(context.Background(), "alice", testParams("alice")); err != nil {
		t.Fatal(err)
	}
	notice := Notice{OccurredAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), EventID: "evt.dup", QueueID: "alice", JobID: "job.1", Mode: "task"}
	if err := mgr.Deliver(context.Background(), "alice", "alice", notice); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Deliver(context.Background(), "alice", "alice", notice); err != nil {
		t.Fatal(err)
	}
	status, _ := mgr.Status("alice", "alice")
	if status.Attempts != 1 {
		t.Fatalf("duplicate ack should not increment attempts: %#v", status)
	}
}

func TestSecretRotationGrace(t *testing.T) {
	var seen [][]string
	mgr, _, close, _ := withFixtureServerURL(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, maxResponseBytes+1))
		if strings.Contains(string(body), "verification") {
			var payload map[string]string
			_ = json.Unmarshal(body, &payload)
			_ = json.NewEncoder(w).Encode(map[string]string{"challenge": payload["challenge"]})
			return
		}
		seen = append(seen, strings.Fields(r.Header.Get("webhook-signature")))
		w.WriteHeader(http.StatusOK)
	})
	defer close()
	first := testParams("alice")
	if _, err := mgr.Subscribe(context.Background(), "alice", first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.Delivery.Secret = "whsec_" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("b"), 32))
	if _, err := mgr.Subscribe(context.Background(), "alice", second); err != nil {
		t.Fatal(err)
	}
	notice := Notice{OccurredAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), EventID: "evt.rotate", QueueID: "alice", JobID: "job.2", Mode: "answer"}
	if err := mgr.Deliver(context.Background(), "alice", "alice", notice); err != nil {
		t.Fatal(err)
	}
	if len(seen) == 0 || len(seen[0]) < 2 {
		t.Fatalf("expected dual signatures during grace, got %#v", seen)
	}
}

func TestDisabledBlocksDelivery(t *testing.T) {
	mgr, _, close, _ := withFixtureServerURL(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, maxResponseBytes+1))
		if strings.Contains(string(body), "verification") {
			var payload map[string]string
			_ = json.Unmarshal(body, &payload)
			_ = json.NewEncoder(w).Encode(map[string]string{"challenge": payload["challenge"]})
		}
		w.WriteHeader(http.StatusOK)
	})
	defer close()
	if _, err := mgr.Subscribe(context.Background(), "alice", testParams("alice")); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Deliver(context.Background(), "alice", "alice", Notice{OccurredAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), EventID: "evt.off", QueueID: "alice", JobID: "job.1", Mode: "answer"}); asEventsError(err).Code != "disabled" {
		t.Fatalf("expected disabled, got %v", err)
	}
}

func TestConcurrentSubscribeAndDeliver(t *testing.T) {
	mgr, dir, close, _ := withFixtureServerURL(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, maxResponseBytes+1))
		if strings.Contains(string(body), "verification") {
			var payload map[string]string
			_ = json.Unmarshal(body, &payload)
			_ = json.NewEncoder(w).Encode(map[string]string{"challenge": payload["challenge"]})
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	defer close()
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	other, err := Open(abs, testKey, mgr.delivery)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := mgr.Subscribe(context.Background(), "alice", testParams("alice"))
			if err != nil && asEventsError(err).Code != "stale" {
				t.Errorf("subscribe: %v", err)
			}
		}()
	}
	wg.Wait()
	notice := Notice{OccurredAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), EventID: "evt.race", QueueID: "alice", JobID: "job.race", Mode: "answer"}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := other.Deliver(context.Background(), "alice", "alice", notice); err != nil && asEventsError(err).Code != "in_flight" {
				t.Errorf("deliver: %v", err)
			}
		}()
	}
	wg.Wait()
	status, _ := mgr.Status("alice", "alice")
	if !status.Acknowledged || status.Attempts != 1 {
		t.Fatalf("race status %#v", status)
	}
}
