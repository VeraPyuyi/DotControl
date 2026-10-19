package dotqueue

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestContractAllIDFieldsAndTenants(t *testing.T) {
	s, _ := openTestStore(t)
	j := mustSubmit(t, s, "ids")
	for _, id := range []string{"", "../escape", strings.Repeat("x", 129), "x/y", "x\x00y"} {
		for name, op := range map[string]func() error{
			"client": func() error { _, e := s.Submit(id, sampleSubmission("good")); return e }, "request": func() error { _, e := s.Submit("client.a", sampleSubmission(id)); return e }, "session": func() error {
				sub := sampleSubmission("session")
				sub.SessionID = id
				if id == "" {
					sub.SessionID = "bad/path"
				}
				_, e := s.Submit("client.a", sub)
				return e
			}, "queue": func() error { _, e := s.Claim(id, j.ID, "worker"); return e }, "worker": func() error { _, e := s.Claim("client.a", j.ID, id); return e }, "job": func() error { _, e := s.Get("client.a", id); return e }} {
			if e := op(); e == nil {
				t.Errorf("%s accepted %q", name, id)
			}
		}
	}
	if _, e := s.Get("client.b", j.ID); e == nil {
		t.Error("tenant Get")
	}
	if _, e := s.ByRequest("client.b", j.RequestID); e == nil {
		t.Error("tenant ByRequest")
	}
	if _, e := s.Cancel("client.b", j.ID); e == nil {
		t.Error("tenant Cancel")
	}
	if _, e := s.Claim("client.b", j.ID, "worker"); e == nil {
		t.Error("tenant Claim")
	}
	if ns, e := s.Pending("client.b"); e != nil || len(ns) != 0 {
		t.Error("tenant Pending")
	}
}
func TestContractCapacityLiteralAndRetainedRequests(t *testing.T) {
	s, dir := openTestStore(t)
	first := mustSubmit(t, s, "capacity.0")
	for i := 1; i < 100; i++ {
		mustSubmit(t, s, fmt.Sprintf("capacity.%d", i))
	}
	if _, e := s.Cancel("client.a", first.ID); e != nil {
		t.Fatal(e)
	}
	s = reopen(t, dir)
	if _, e := s.Submit("client.a", sampleSubmission("overflow")); e == nil {
		t.Error("101st job accepted")
	}
	j, e := s.Submit("client.a", sampleSubmission("capacity.0"))
	if e != nil || j.ID != first.ID {
		t.Errorf("retained request: %v %#v", e, j)
	}
	sub := sampleSubmission("capacity.0")
	sub.Messages[0].Content = "different"
	if _, e = s.Submit("client.a", sub); e == nil {
		t.Error("retained request reused")
	}
}
func TestContractTwoStoreSameRequestRace(t *testing.T) {
	s, dir := openTestStore(t)
	other := reopen(t, dir)
	var wg sync.WaitGroup
	var jobs [2]Job
	var errs [2]error
	start := make(chan struct{})
	for i, store := range []*Store{s, other} {
		wg.Add(1)
		go func(i int, store *Store) {
			defer wg.Done()
			<-start
			jobs[i], errs[i] = store.Submit("client.a", sampleSubmission("same"))
		}(i, store)
	}
	close(start)
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || jobs[0].ID != jobs[1].ID {
		t.Fatalf("CAS jobs %#v errors %v", jobs, errs)
	}
}
func TestContractAuthenticatedNestedAndLifecycle(t *testing.T) {
	for _, kind := range []string{"result-extra", "artifact-extra", "result-null", "text-null", "token", "worker", "result-digest", "result-mismatch", "result-cancel", "index-extra", "duplicate-order", "null-job", "over-capacity"} {
		t.Run(kind, func(t *testing.T) {
			s, _ := openTestStore(t)
			j := mustSubmit(t, s, "nested")
			c, e := s.Claim("client.a", j.ID, "worker")
			if e != nil {
				t.Fatal(e)
			}
			if _, e = s.Complete(j.ID, c.Token, Result{Text: "done", Artifacts: []Artifact{{URL: "https://example.com/a", Label: "a"}}}); e != nil {
				t.Fatal(e)
			}
			authenticatedFixture(t, s, func(m map[string]any) {
				jobs := m["jobs"].(map[string]any)
				r := jobs[j.ID].(map[string]any)
				switch kind {
				case "result-extra":
					r["result"].(map[string]any)["extra"] = true
				case "artifact-extra":
					r["result"].(map[string]any)["artifacts"].([]any)[0].(map[string]any)["extra"] = true
				case "result-null":
					r["result"] = nil
				case "text-null":
					r["result"].(map[string]any)["text"] = nil
				case "token":
					r["claim_token"] = ""
				case "worker":
					r["worker_id"] = "bad/id"
				case "result-digest":
					r["result_digest"] = strings.Repeat("a", 64)
				case "result-mismatch":
					r["state"] = "failed"
				case "result-cancel":
					r["cancel_requested"] = false
					r["state"] = "cancelled"
				case "index-extra":
					m["request_index"].(map[string]any)["extra"] = j.ID
				case "duplicate-order":
					m["order"] = []any{j.ID, j.ID}
				case "null-job":
					jobs[j.ID] = nil
				case "over-capacity":
					for i := 0; i < 101; i++ {
						jobs[fmt.Sprint(i)] = r
					}
				}
			})
			if _, e = s.Get("client.a", j.ID); e == nil || e.Error() != "dot_queue_corrupt" {
				t.Errorf("invalid state %v", e)
			}
		})
	}
	s, _ := openTestStore(t)
	j := mustSubmit(t, s, "raw-json")
	for _, suffix := range []string{" {}", "\nnull"} {
		b, _ := os.ReadFile(filepath.Join(s.dir, stateFileName))
		plain, e := openEnvelope(testKey, b)
		if e != nil {
			t.Fatal(e)
		}
		bad, _ := seal(testKey, append(plain, []byte(suffix)...))
		os.WriteFile(filepath.Join(s.dir, stateFileName), bad, 0600)
		if _, e = s.Get("client.a", j.ID); e == nil {
			t.Error("trailing JSON accepted")
		}
		os.WriteFile(filepath.Join(s.dir, stateFileName), b, 0600)
	}
}
func TestContractArtifactLocalAliases(t *testing.T) {
	for _, u := range []string{"https://127.1/a", "https://2130706433/a", "https://100.64.0.1/a", "https://example.com:0/a", "https://example.com:99999/a"} {
		if validateArtifact(Artifact{URL: u}) == nil {
			t.Errorf("local/invalid artifact accepted %s", u)
		}
	}
}
func TestContractBoundsMetadataAndEnvelope(t *testing.T) {
	for _, n := range []int{32, 33} {
		sub := sampleSubmission("messages")
		sub.Messages = make([]Message, n)
		for i := range sub.Messages {
			sub.Messages[i] = Message{Role: "user", Content: "a"}
		}
		if e := validateSubmission(sub); (e == nil) != (n == 32) {
			t.Errorf("message count %d %v", n, e)
		}
	}
	for _, n := range []int{16, 17} {
		r := Result{Artifacts: make([]Artifact, n)}
		for i := range r.Artifacts {
			r.Artifacts[i] = Artifact{URL: "https://example.com", Label: "a"}
		}
		if e := validateResult(r); (e == nil) != (n == 16) {
			t.Errorf("artifacts %d %v", n, e)
		}
	}
	for _, n := range []int{256, 257} {
		if e := validateArtifact(Artifact{URL: "https://example.com", Label: strings.Repeat("a", n)}); (e == nil) != (n == 256) {
			t.Errorf("label %d %v", n, e)
		}
	}
	for _, n := range []int{2048, 2049} {
		u := "https://example.com/"
		u += strings.Repeat("a", n-len(u))
		if e := validateArtifact(Artifact{URL: u}); (e == nil) != (n == 2048) {
			t.Errorf("URL %d %v", n, e)
		}
	}
	r := Result{Text: strings.Repeat("a", 262144-len("https://example.com")-256), Artifacts: []Artifact{{URL: "https://example.com", Label: strings.Repeat("a", 256)}}}
	if e := validateResult(r); e != nil {
		t.Fatal(e)
	}
	r.Text += "a"
	if validateResult(r) == nil {
		t.Error("artifact budget bypass")
	}
	b, e := seal(testKey, []byte("secret"))
	if e != nil {
		t.Fatal(e)
	}
	b2, e := seal(testKey, []byte("secret"))
	if e != nil || bytes.Equal(b, b2) {
		t.Error("nonce reused")
	}
	b[1] = 2
	if _, e = openEnvelope(testKey, b); e == nil {
		t.Error("envelope version")
	}
	wrong := append([]byte{}, testKey...)
	wrong[0] ^= 1
	if _, e = openEnvelope(wrong, b2); e == nil {
		t.Error("wrong key accepted")
	}
}
func TestContractConfigAndPaths(t *testing.T) {
	s, dir := openTestStore(t)
	if e := s.SetEnabled(false); e != nil {
		t.Fatal(e)
	}
	cfg, e := reopen(t, dir).ConfigStatus()
	if e != nil || cfg.Enabled {
		t.Errorf("config %v %#v", e, cfg)
	}
	for _, path := range []string{"relative", filepath.VolumeName(dir) + string(filepath.Separator), dir + string(filepath.Separator) + ".."} {
		if _, e := Open(path, testKey); e == nil {
			t.Errorf("unsafe dir %q", path)
		}
	}
	for _, n := range []int{0, 31, 33} {
		if _, e := Open(dir, make([]byte, n)); e == nil {
			t.Error("bad key accepted")
		}
	}
}
func TestContractUnknownCancelIntent(t *testing.T) {
	s, _ := openTestStore(t)
	j := mustSubmit(t, s, "unknown-cancel")
	c, e := s.Claim("client.a", j.ID, "worker")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Recover(); e != nil {
		t.Fatal(e)
	}
	j, e = s.Cancel("client.a", j.ID)
	if e != nil || !j.CancelRequested || j.State != "outcome_unknown" {
		t.Fatalf("unknown cancel intent: %v %#v", e, j)
	}
	if _, e = s.Complete(j.ID, c.Token, Result{Cancelled: true}); e != nil {
		t.Fatal(e)
	}
}
func TestContractOwnFileHardlinks(t *testing.T) {
	for _, name := range []string{lockName, stateFileName} {
		t.Run(name, func(t *testing.T) {
			s, dir := openTestStore(t)
			j := mustSubmit(t, s, "hardlink")
			if e := os.Link(filepath.Join(dir, name), filepath.Join(dir, "alias")); e != nil {
				t.Fatal(e)
			}
			if _, e := s.Get("client.a", j.ID); e == nil {
				t.Error("hardlink accepted")
			}
		})
	}
}
func TestContractDuplicateKeys(t *testing.T) {
	s, dir := openTestStore(t)
	j := mustSubmit(t, s, "duplicate-json")
	b, _ := os.ReadFile(filepath.Join(dir, stateFileName))
	p, _ := openEnvelope(testKey, b)
	var m map[string]json.RawMessage
	if e := json.Unmarshal(p, &m); e != nil {
		t.Fatal(e)
	}
	p = append([]byte(`{"enabled":true,`), p[1:]...)
	b, _ = seal(testKey, p)
	os.WriteFile(filepath.Join(dir, stateFileName), b, 0600)
	if _, e := s.Get("client.a", j.ID); e == nil {
		t.Error("duplicate keys accepted")
	}
}
