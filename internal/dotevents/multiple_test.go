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

func TestMultipleSubscriptionsAndRevokeDuringDelivery(t *testing.T) {
	var sends atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	var block atomic.Bool
	m, _, stop, _ := withFixtureServerURL(t, func(w http.ResponseWriter, r *http.Request) {
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
		if block.Load() {
			close(entered)
			<-release
		}
		w.WriteHeader(204)
	})
	defer stop()
	p := testParams("alice")
	if _, e := m.Subscribe(context.Background(), "alice", p); e != nil {
		t.Fatal(e)
	}
	second := p
	second.Delivery.URL += "&second=1"
	if _, e := m.Subscribe(context.Background(), "alice", second); e != nil {
		t.Fatal(e)
	}
	if e := m.Deliver(context.Background(), "alice", "alice", Notice{"evt.multi", "alice", "job.multi", "answer"}); e != nil {
		t.Fatal(e)
	}
	if sends.Load() != 2 {
		t.Fatalf("sent to %d subscriptions", sends.Load())
	}
	if e := m.Unsubscribe(context.Background(), "alice", second); e != nil {
		t.Fatal(e)
	}
	block.Store(true)
	done := make(chan error, 1)
	go func() {
		done <- m.Deliver(context.Background(), "alice", "alice", Notice{"evt.block", "alice", "job.multi", "task"})
	}()
	<-entered
	if e := m.SetEnabled(false); e != nil {
		t.Fatal(e)
	}
	if e := m.SetEnabled(true); e != nil {
		t.Fatal(e)
	}
	close(release)
	if e := <-done; e == nil {
		t.Fatal("late callback acknowledged after revoke")
	}
	s, _ := m.Status("alice", "alice")
	if s.CallbackVerified {
		t.Fatal("pause revived verification")
	}
}
func TestTTLBoundsAndRotationExpiry(t *testing.T) {
	for _, raw := range []string{`"60000"`, `true`, `-1`, `86400001`, `{}`, `[]`} {
		if _, e := parseTTL(json.RawMessage(raw)); e == nil {
			t.Errorf("TTL accepted %s", raw)
		}
	}
	sub := &subscriptionRecord{Secret: testSecret(), PreviousSecret: testSecret(), RotateUntil: time.Now().Add(-time.Second)}
	if len(signPayload(sub, "id", []byte("中文"), 1)) != 1 {
		t.Fatal("old secret signed after grace")
	}
}
