package dotevents

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestOfficialWireOccurrence(t *testing.T) {
	var occurrence map[string]json.RawMessage
	m, _, close, _ := withFixtureServerURL(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var v map[string]json.RawMessage
		_ = json.Unmarshal(b, &v)
		if string(v["type"]) == `"verification"` {
			var c string
			_ = json.Unmarshal(v["challenge"], &c)
			_ = json.NewEncoder(w).Encode(map[string]string{"challenge": c})
			return
		}
		occurrence = v
		w.WriteHeader(204)
	})
	defer close()
	if len(m.List("alice")) != 1 {
		t.Error("event discovery unavailable before subscription")
	}
	if _, e := m.Subscribe(context.Background(), "alice", testParams("alice")); e != nil {
		t.Fatal(e)
	}
	if e := m.Deliver(context.Background(), "alice", "alice", Notice{EventID: "evt.official", QueueID: "alice", JobID: "job.official", Mode: "answer", OccurredAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}); e != nil {
		t.Fatal(e)
	}
	for _, k := range []string{"eventId", "name", "timestamp", "data", "cursor"} {
		if _, ok := occurrence[k]; !ok {
			t.Errorf("missing official occurrence field %s", k)
		}
	}
	if _, ok := occurrence["type"]; ok {
		t.Error("occurrence used control type")
	}
	if string(occurrence["cursor"]) != "null" {
		t.Error("cursor must be null")
	}
}
