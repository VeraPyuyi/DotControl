package dotbridge

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/dotevents"
	"github.com/dotcontrol/dotcontrol/internal/dotqueue"
)

func TestLegacyLeaseStatusAndAuthenticatedRenewalAfterRestart(t *testing.T) {
	s, token, id, legacy := openLegacyStatusBridge(t)
	eventsPath := filepath.Join(s.root, "events", "events.state.de")
	bridgePath := filepath.Join(s.root, "bridge.state.json")
	eventsBefore, bridgeBefore := mustRead(eventsPath), mustRead(bridgePath)
	policy := s.state.Clients[id]
	api := httptest.NewServer(s.Handler())
	defer api.Close()

	code, raw := listHTTPRequest(t, api, http.MethodGet, "/dot-bridge/v1/status", token, nil)
	var status StatusDTO
	if code != http.StatusOK || json.Unmarshal(raw, &status) != nil {
		t.Fatalf("status: %d %s", code, raw)
	}
	if status.Subscription.State != "active" || status.Subscription.ExpiresAt == nil || !status.Subscription.ExpiresAt.Equal(legacy.Expires) || status.Subscription.RefreshBefore == nil || !status.Subscription.RefreshBefore.Equal(legacy.Expires) || status.Subscription.LastRefreshedAt == nil || !status.Subscription.LastRefreshedAt.Equal(legacy.Refreshed) {
		t.Errorf("verified legacy lease missing from public status: %#v", status.Subscription)
	}
	if status.SubscribeFailure != nil || status.Subscription.RenewalState != "current" {
		t.Errorf("obsolete failure still reported: %#v", status)
	}
	if status.Ready || status.Code != "probe_required" || status.Capabilities.Text || status.Capabilities.Tasks || status.VerifiedAt != nil || status.ExpiresAt != nil {
		t.Errorf("legacy proof became ready after restart: %#v", status)
	}
	for _, private := range []string{testEventsSecret(), fixtureCallbackURL, legacy.ID, policy.TokenHash} {
		if strings.Contains(string(raw), private) {
			t.Error("public status leaked private binding data")
		}
	}
	assertLegacyWorkRefused(t, s, id)
	if _, err := s.StartProbe(id); err == nil || err.Error() != "subscription_required" {
		t.Fatalf("probe adopted unauthenticated legacy binding: %v", err)
	}
	if len(s.subscriptions) != 0 || s.state.Clients[id] != policy || !bytes.Equal(eventsBefore, mustRead(eventsPath)) || !bytes.Equal(bridgeBefore, mustRead(bridgePath)) {
		t.Fatal("read or rejected work mutated legacy authorization")
	}

	// Only a real authenticated eventsSubscribe may create the new epoch and
	// bridge binding. Even an unexpired consumed legacy proof cannot follow it.
	renewed, err := s.EventsSubscribe(context.Background(), s.mcpQueue, testEventsParams(id))
	if err != nil {
		t.Fatal(err)
	}
	if !renewed.CallbackVerified || renewed.TrustEpoch == "" || renewed.ID != legacy.ID {
		t.Fatalf("authenticated renewal did not upgrade legacy grant: %#v", renewed)
	}
	sub := s.subscriptions[id]
	if sub.TrustEpoch != renewed.TrustEpoch || sub.BindingID != legacy.ID || sub.AuthHash != policy.TokenHash || sub.Consumer != s.mcpQueue {
		t.Fatalf("renewal missing authenticated binding: %#v", sub)
	}
	status, err = s.StatusForClient(id)
	if err != nil || status.Ready || status.Code != "probe_required" || status.Subscription.State != "active" || status.Capabilities.Text || status.Capabilities.Tasks {
		t.Fatalf("renewal promoted legacy proof: %#v %v", status, err)
	}
	assertLegacyWorkRefused(t, s, id)
	proveSustainable(t, s, id)
	status, err = s.StatusForClient(id)
	if err != nil || !status.Ready || !status.Capabilities.Text || status.Capabilities.Tasks {
		t.Fatalf("fresh answer probe did not establish only answer capability: %#v %v", status, err)
	}
}

func assertLegacyWorkRefused(t *testing.T, s *Service, id string) {
	t.Helper()
	before, err := s.queue.List(id)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{dotqueue.ModeAnswer, dotqueue.ModeTask} {
		req := SubmitRequest{RequestID: "legacy." + mode, Mode: mode, SessionID: "legacy", Task: "must not run"}
		if mode == dotqueue.ModeAnswer {
			req.Task = ""
			req.Messages = []MessageDTO{{Role: "user", Content: "must not run"}}
		}
		_, _, err := s.submit(s.state.Clients[id], req)
		want := "probe_required"
		if mode == dotqueue.ModeTask {
			want = "task_probe_required"
		}
		if err == nil || err.Error() != want {
			t.Errorf("legacy %s submission: %v, want %s", mode, err, want)
		}
	}
	after, err := s.queue.List(id)
	if err != nil || len(after) != len(before) {
		t.Fatalf("unready submission created jobs: before=%d after=%d err=%v", len(before), len(after), err)
	}
}

// Build only temporary encrypted state. Relative times match a grant created at
// 01:45, renewed at 04:02, and inspected after a 04:22 upgrade with a 05:02 expiry.
func openLegacyStatusBridge(t *testing.T) (*Service, string, string, dotevents.TrustedBinding) {
	t.Helper()
	s, token, id := openTestBridge(t)
	proveSustainable(t, s, id)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	legacy := dotevents.TrustedBinding{ID: s.subscriptions[id].BindingID, Refreshed: now.Add(-20 * time.Minute), Expires: now.Add(40*time.Minute - time.Second)}
	policy := s.state.Clients[id]
	proof := policy.proof(dotqueue.ModeAnswer)
	proof.TrustEpoch = ""
	proof.Verified, proof.Expires = now.Add(-time.Minute), now.Add(9*time.Minute)
	proof.SubscriptionExpires = legacy.Expires
	policy.setProof(dotqueue.ModeAnswer, proof)
	policy.setProof(dotqueue.ModeTask, proof)
	policy.Capabilities = CapPolicy{Text: true, Tasks: true}
	s.state.Clients[id] = policy
	delete(s.subscriptions, id) // Old binaries never persisted this ephemeral map.
	if err := s.saveState(); err != nil {
		t.Fatal(err)
	}

	p := testEventsParams(id)
	p.TTL = []byte("3600000")
	plain, err := json.Marshal(map[string]any{
		"state_version": 1, "enabled": true,
		"subscriptions": map[string]any{legacy.ID: map[string]any{
			"id": legacy.ID, "principal": id, "params": p, "secret": p.Delivery.Secret,
			"generation": "legacy-generation", "callback_verified": true,
			"created_at": now.Add(-157 * time.Minute), "updated_at": legacy.Refreshed,
			"expires_at": legacy.Expires, "verified_until": legacy.Refreshed.Add(5 * time.Minute),
		}},
		"deliveries": map[string]any{}, "queue_status": map[string]any{},
		"last_subscribe": map[string]any{"at": now.Add(-65 * time.Minute), "principal": id, "reason": "network", "stage": "network"},
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := s.vault.Get(StateKeyName)
	if err != nil {
		t.Fatal(err)
	}
	key, err := decodeStateKey(encoded)
	if err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	sealed := append([]byte{'E', 1}, nonce...)
	sealed = gcm.Seal(sealed, nonce, plain, nil)
	if err := os.WriteFile(filepath.Join(s.root, "events", "events.state.de"), sealed, 0600); err != nil {
		t.Fatal(err)
	}
	delivery, stop := NewTestEventsDelivery(t)
	t.Cleanup(stop)
	reopened, err := Open(context.Background(), Config{Root: s.root, Listen: s.listen, Vault: s.vault, MCPQueue: s.mcpQueue, EventsDelivery: delivery})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	return reopened, token, id, legacy
}
