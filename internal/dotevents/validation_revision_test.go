package dotevents

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
)

func TestCompleteFixtureValidationZeroIO(t *testing.T) {
	var calls atomic.Int32
	m, _, stop, _ := withFixtureServerURL(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); echoVerification(w, r) })
	defer stop()
	cases := []struct {
		name      string
		mutate    func(*Params)
		principal string
	}{
		{"principal_queue_mismatch", func(p *Params) { p.Arguments.QueueID = "bob" }, "alice"},
		{"invalid_principal", func(*Params) {}, "bad principal"},
		{"event", func(p *Params) { p.Name = "other" }, "alice"},
		{"secret", func(p *Params) { p.Delivery.Secret = "whsec_bad" }, "alice"},
		{"ttl_string", func(p *Params) { p.TTL = json.RawMessage(`"60000"`) }, "alice"},
		{"ttl_small", func(p *Params) { p.TTL = json.RawMessage(`1`) }, "alice"},
		{"cursor", func(p *Params) { p.Cursor = json.RawMessage(`"resume"`) }, "alice"},
		{"private", func(p *Params) { p.Delivery.URL = "https://127.0.0.1/" }, "alice"},
		{"fragment", func(p *Params) { p.Delivery.URL += "#x" }, "alice"},
		{"mode", func(p *Params) { p.Delivery.Mode = "stream" }, "alice"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := testParams("alice")
			c.mutate(&p)
			_, e := m.Subscribe(context.Background(), c.principal, p)
			want := "unknown"
			if c.name == "private" || c.name == "fragment" {
				want = "callback"
			}
			if e == nil || asEventsError(e).Code != want {
				t.Fatalf("validation error %v", e)
			}
			if calls.Load() != 0 {
				t.Fatal("invalid subscription caused I/O")
			}
		})
	}
	if _, e := m.Subscribe(context.Background(), "alice", testParams("alice")); e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 1 {
		t.Fatal("valid control did not reach callback")
	}
}
func TestCompleteFixtureStrictMutations(t *testing.T) {
	valid, _ := json.Marshal(testParams("alice"))
	var base map[string]any
	_ = json.Unmarshal(valid, &base)
	for _, c := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"unknown_top", func(v map[string]any) { v["extra"] = true }},
		{"unknown_arguments", func(v map[string]any) { v["arguments"].(map[string]any)["extra"] = true }},
		{"null_queue", func(v map[string]any) { v["arguments"].(map[string]any)["queue_id"] = nil }},
		{"unknown_delivery", func(v map[string]any) { v["delivery"].(map[string]any)["extra"] = true }},
		{"null_url", func(v map[string]any) { v["delivery"].(map[string]any)["url"] = nil }},
		{"case_name", func(v map[string]any) { v["Name"] = v["name"]; delete(v, "name") }},
	} {
		t.Run(c.name, func(t *testing.T) {
			var v map[string]any
			_ = json.Unmarshal(valid, &v)
			c.mutate(v)
			b, _ := json.Marshal(v)
			var p Params
			if json.Unmarshal(b, &p) == nil {
				t.Fatal("mutated complete fixture accepted")
			}
		})
	}
	var p Params
	if e := json.Unmarshal(valid, &p); e != nil {
		t.Fatalf("complete control failed %v", e)
	}
	duplicate := append([]byte(`{"name":"other",`), valid[1:]...)
	if json.Unmarshal(duplicate, &p) == nil {
		t.Fatal("duplicate accepted")
	}
}
