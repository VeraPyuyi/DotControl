package dotbridge

import (
	"context"
	"testing"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/dotqueue"
)

func TestRestoredExpiredBridgeLeaseCannotAdoptEventRenewal(t *testing.T) {
	s, _, id := openTestBridge(t)
	proveSustainable(t, s, id)
	policy := s.state.Clients[id]
	accepted, err := s.queue.Submit(id, dotqueue.Submission{RequestID: "accepted.before.expiry", Mode: dotqueue.ModeAnswer, SessionID: "recovery", Messages: []dotqueue.Message{{Role: "user", Content: "accepted"}}})
	if err != nil {
		t.Fatal(err)
	}
	// Model event renewal committed with the same epoch, while the bridge still
	// has its previous committed lease (now expired).
	if _, err = s.events.Subscribe(context.Background(), id, testEventsParams(id)); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	sub := s.subscriptions[id]
	sub.Expires = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	s.subscriptions[id] = sub
	err = s.saveState()
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(context.Background(), Config{Root: s.root, Listen: s.listen, Vault: s.vault, MCPQueue: s.mcpQueue})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	status, err := restored.StatusForClient(id)
	if err != nil {
		t.Fatal(err)
	}
	if status.Ready || status.Code != "subscription_expired" {
		t.Errorf("expired committed lease became ready: %#v", status)
	}
	before, err := restored.queue.List(id)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = restored.submit(policy, SubmitRequest{RequestID: "new.after.expiry", Mode: dotqueue.ModeAnswer, SessionID: "new", Messages: []MessageDTO{{Role: "user", Content: "must refuse"}}})
	if err == nil {
		t.Error("new submission accepted expired bridge lease")
	}
	after, err := restored.queue.List(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Error("unready submission created a job")
	}
	recovered, err := restored.getJob(policy, accepted.ID)
	if err != nil || recovered.JobID != accepted.ID {
		t.Fatalf("accepted job recovery: %#v %v", recovered, err)
	}
}
