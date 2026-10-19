package dotevents

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestReviewIdentityTimestampAndCache(t *testing.T) {
	var challenges atomic.Int32
	var bodies [][]byte
	m, dir, stop, _ := withFixtureServerURL(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var v map[string]json.RawMessage
		_ = json.Unmarshal(b, &v)
		if string(v["type"]) == `"verification"` {
			challenges.Add(1)
			var c string
			_ = json.Unmarshal(v["challenge"], &c)
			_ = json.NewEncoder(w).Encode(map[string]string{"challenge": c})
			return
		}
		var id string
		_ = json.Unmarshal(v["eventId"], &id)
		if r.Header.Get("webhook-id") != id {
			t.Error("webhook-id differs from body eventId")
		}
		bodies = append(bodies, b)
		w.WriteHeader(500)
	})
	defer stop()
	p := testParams("alice")
	for i := 0; i < 2; i++ {
		if _, e := m.Subscribe(context.Background(), "alice", p); e != nil {
			t.Fatal(e)
		}
	}
	if challenges.Load() != 1 {
		t.Errorf("refresh challenges=%d", challenges.Load())
	}
	n := Notice{OccurredAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), EventID: "evt.original", QueueID: "alice", JobID: "job.original", Mode: "task"}
	_ = m.Deliver(context.Background(), "alice", "alice", n)
	reopened, e := Open(dir, testKey, m.delivery)
	if e != nil {
		t.Fatal(e)
	}
	_ = m.store.withLock(false, true, func(s *persistenceState) error {
		for _, r := range s.Deliveries {
			r.NextAttempt = time.Time{}
		}
		return nil
	})
	_ = reopened.Deliver(context.Background(), "alice", "alice", n)
	if len(bodies) != 2 || string(bodies[0]) != string(bodies[1]) {
		t.Fatal("reopened retry body changed")
	}
}
func TestReviewNewStoreDisabledAndMarker(t *testing.T) {
	m, e := Open(t.TempDir(), testKey, nil)
	if e != nil {
		t.Fatal(e)
	}
	_, e = m.Subscribe(context.Background(), "alice", testParams("alice"))
	if e == nil || asEventsError(e).Code != "disabled" {
		t.Errorf("new store enabled: %v", e)
	}
	if e = os.WriteFile(filepath.Join(m.store.dir, markerName), []byte("bad"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = m.Status("alice", "alice"); e == nil {
		t.Error("invalid marker accepted")
	}
}
func TestReviewPaths(t *testing.T) {
	base := t.TempDir()
	for _, p := range []string{filepath.VolumeName(base) + string(os.PathSeparator), base + string(os.PathSeparator) + ".", filepath.Join(base, "x") + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "y"} {
		if _, e := Open(p, testKey, nil); e == nil {
			t.Errorf("unsafe path accepted %s", p)
		}
	}
	target := t.TempDir()
	link := filepath.Join(base, "link")
	if e := os.Symlink(target, link); e != nil {
		t.Logf("symlink unavailable: %v", e)
		return
	}
	if _, e := Open(filepath.Join(link, "events"), testKey, nil); e == nil {
		t.Fatal("linked ancestor accepted")
	}
}
