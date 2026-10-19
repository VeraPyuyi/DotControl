package dotqueue

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func mustSubmit(t *testing.T, s *Store, id string) Job {
	t.Helper()
	j, e := s.Submit("client.a", sampleSubmission(id))
	if e != nil {
		t.Fatal(e)
	}
	return j
}
func reopen(t *testing.T, dir string) *Store {
	t.Helper()
	s, e := Open(dir, testKey)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func TestReviewTokensAndUnclaimedComplete(t *testing.T) {
	s, _ := openTestStore(t)
	j := mustSubmit(t, s, "token")
	for _, token := range []string{"", "x", strings.Repeat("a", 64)} {
		if _, e := s.Complete(j.ID, token, Result{Text: "forged"}); e == nil {
			t.Errorf("unclaimed Complete accepted token %q", token)
		}
	}
	got, e := s.Get("client.a", j.ID)
	if e != nil || got.State != "queued" {
		t.Fatalf("unclaimed job changed: %v %#v", e, got)
	}
	c, e := s.Claim("client.a", j.ID, "worker")
	if e != nil {
		t.Fatal(e)
	}
	for _, token := range []string{"", "x", strings.Repeat("f", 64), strings.ToUpper(c.Token)} {
		if token == c.Token {
			continue
		}
		if _, e := s.Progress(j.ID, token, "running", "bad"); e == nil {
			t.Error("wrong Progress token accepted")
		}
		if _, e := s.Complete(j.ID, token, Result{}); e == nil {
			t.Error("wrong Complete token accepted")
		}
	}
}
func TestReviewUnknownReconcileAndSummary(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(fmt.Sprint(cancel), func(t *testing.T) {
			s, dir := openTestStore(t)
			j := mustSubmit(t, s, "recover")
			c, e := s.Claim("client.a", j.ID, "worker")
			if e != nil {
				t.Fatal(e)
			}
			p, e := s.Progress(j.ID, c.Token, "claimed", "durable summary")
			if e != nil {
				t.Error(e)
			}
			v := reflect.ValueOf(p).FieldByName("Summary")
			if !v.IsValid() || v.String() != "durable summary" {
				t.Error("summary not observable")
			}
			s = reopen(t, dir)
			g, e := s.Get("client.a", j.ID)
			if e != nil {
				t.Fatal(e)
			}
			v = reflect.ValueOf(g).FieldByName("Summary")
			if !v.IsValid() || v.String() != "durable summary" {
				t.Error("summary not persisted")
			}
			if cancel {
				if _, e = s.Cancel("client.a", j.ID); e != nil {
					t.Fatal(e)
				}
			}
			if e = s.Recover(); e != nil {
				t.Fatal(e)
			}
			before, _ := s.Get("client.a", j.ID)
			if e = s.Recover(); e != nil {
				t.Fatal(e)
			}
			after, _ := s.Get("client.a", j.ID)
			if !before.UpdatedAt.Equal(after.UpdatedAt) {
				t.Error("recovery repeated")
			}
			if _, e = s.Claim("client.a", j.ID, "new"); e == nil {
				t.Error("unknown reclaimed")
			}
			r := Result{Text: "authoritative secret result", Cancelled: cancel}
			done, e := s.Complete(j.ID, c.Token, r)
			if e != nil {
				t.Fatalf("former token reconciliation: %v", e)
			}
			want := "completed"
			if cancel {
				want = "cancelled"
			}
			if done.State != want {
				t.Error(done.State)
			}
			s = reopen(t, dir)
			done, e = s.Get("client.a", j.ID)
			if e != nil || done.Result == nil || done.Result.Text != r.Text {
				t.Fatalf("terminal persistence: %v %#v", e, done)
			}
			if _, e = s.Complete(j.ID, c.Token, r); e != nil {
				t.Error(e)
			}
			r.Text = "different"
			if _, e = s.Complete(j.ID, c.Token, r); e == nil {
				t.Error("conflicting terminal retry")
			}
			entries, _ := os.ReadDir(dir)
			for _, entry := range entries {
				b, _ := os.ReadFile(filepath.Join(dir, entry.Name()))
				for _, secret := range []string{"hello dot", "durable summary", "authoritative secret result", c.Token} {
					if bytes.Contains(b, []byte(secret)) {
						t.Errorf("plaintext %s", entry.Name())
					}
				}
			}
		})
	}
}
func TestReviewDisabledAllOperations(t *testing.T) {
	s, dir := openTestStore(t)
	j := mustSubmit(t, s, "disabled-all")
	c, e := s.Claim("client.a", j.ID, "worker")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.SetEnabled(false); e != nil {
		t.Fatal(e)
	}
	s = reopen(t, dir)
	ops := map[string]func() error{"Get": func() error { _, e := s.Get("client.a", j.ID); return e }, "ByRequest": func() error { _, e := s.ByRequest("client.a", j.RequestID); return e }, "Pending": func() error { _, e := s.Pending("client.a"); return e }, "Submit": func() error { _, e := s.Submit("client.a", sampleSubmission("new")); return e }, "Claim": func() error { _, e := s.Claim("client.a", j.ID, "worker"); return e }, "Progress": func() error { _, e := s.Progress(j.ID, c.Token, "running", ""); return e }, "Complete": func() error { _, e := s.Complete(j.ID, c.Token, Result{}); return e }, "Cancel": func() error { _, e := s.Cancel("client.a", j.ID); return e }, "Recover": s.Recover}
	for name, op := range ops {
		t.Run(name, func(t *testing.T) {
			e := op()
			if e == nil || e.Error() != "dot_queue_disabled" {
				t.Fatalf("want disabled got %v", e)
			}
		})
	}
}
func TestReviewLiteralBounds(t *testing.T) {
	for _, n := range []int{262144, 262145} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			s, _ := openTestStore(t)
			sub := Submission{RequestID: "bound", Mode: "task", Task: strings.Repeat("t", n-1), Messages: []Message{{Role: "user", Content: "m"}}}
			_, e := s.Submit("client.a", sub)
			if (e == nil) != (n == 262144) {
				t.Errorf("combined prompt %d: %v", n, e)
			}
			r := Result{Text: strings.Repeat("r", n-1), ErrorCode: "e"}
			if e = validateResult(r); (e == nil) != (n == 262144) {
				t.Errorf("total result %d: %v", n, e)
			}
		})
	}
	for _, sub := range []Submission{{RequestID: "bad", Mode: "task", Task: string([]byte{255})}, {RequestID: "bad", Mode: "answer", Messages: []Message{{Role: "user", Content: string([]byte{255})}}}, {RequestID: "bad", Mode: "invalid"}, {RequestID: "bad", Mode: "answer"}, {RequestID: "bad", Mode: "answer", Messages: []Message{{Role: "tool", Content: "x"}}}} {
		s, _ := openTestStore(t)
		if _, e := s.Submit("client.a", sub); e == nil {
			t.Errorf("bad submission accepted %#v", sub)
		}
	}
	for _, raw := range []string{"https://localhost/a", "https://foo.localhost/a", "https://127.0.0.1/a", "https://10.0.0.1/a", "https://[::1]/a", "https://[fc00::1]/a", "https://169.254.1.1/a", "https://user:pass@example.com/a", "https://example.com/" + strings.Repeat("a", 2049), string([]byte{255})} {
		if e := validateResult(Result{Artifacts: []Artifact{{URL: raw}}}); e == nil {
			t.Errorf("bad artifact accepted %q", raw)
		}
	}
	for _, r := range []Result{{ErrorCode: strings.Repeat("e", 129)}, {ErrorCode: "contains spaces"}, {ErrorCode: string([]byte{255})}, {Text: string([]byte{255})}, {Artifacts: make([]Artifact, 17)}, {Artifacts: []Artifact{{URL: "https://example.com", Label: strings.Repeat("a", 257)}}}} {
		if e := validateResult(r); e == nil {
			t.Errorf("bad result accepted %#v", r)
		}
	}
	for _, n := range []int{128, 129} {
		s, _ := openTestStore(t)
		_, e := s.Submit(strings.Repeat("c", n), sampleSubmission(strings.Repeat("r", n)))
		if (e == nil) != (n == 128) {
			t.Errorf("ID length %d %v", n, e)
		}
	}
	for _, n := range []int{2048, 2049} {
		if e := validateSummary(strings.Repeat("s", n)); (e == nil) != (n == 2048) {
			t.Errorf("summary %d %v", n, e)
		}
	}
}
func authenticatedFixture(t *testing.T, s *Store, mutate func(map[string]any)) {
	t.Helper()
	b, e := os.ReadFile(filepath.Join(s.dir, stateFileName))
	if e != nil {
		t.Fatal(e)
	}
	p, e := openEnvelope(testKey, b)
	if e != nil {
		t.Fatal(e)
	}
	var obj map[string]any
	if e = json.Unmarshal(p, &obj); e != nil {
		t.Fatal(e)
	}
	mutate(obj)
	p, e = json.Marshal(obj)
	if e != nil {
		t.Fatal(e)
	}
	b, e = seal(testKey, p)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(s.dir, stateFileName), b, 0600); e != nil {
		t.Fatal(e)
	}
}
func TestReviewAuthenticatedInvalidState(t *testing.T) {
	cases := map[string]func(map[string]any){"enabled-null": func(m map[string]any) { m["enabled"] = nil }, "enabled-missing": func(m map[string]any) { delete(m, "enabled") }, "version": func(m map[string]any) { m["state_version"] = 2 }, "index-missing": func(m map[string]any) { m["request_index"] = map[string]any{} }}
	jobCases := map[string]func(map[string]any){"nested-unknown": func(j map[string]any) { j["future"] = true }, "state": func(j map[string]any) { j["state"] = "bogus" }, "missing-state": func(j map[string]any) { delete(j, "state") }, "null-state": func(j map[string]any) { j["state"] = nil }, "wrong-type": func(j map[string]any) { j["cancel_requested"] = "false" }, "digest": func(j map[string]any) { j["request_digest"] = strings.Repeat("a", 64) }, "claim": func(j map[string]any) { j["state"] = "claimed" }, "terminal": func(j map[string]any) { j["state"] = "completed" }, "message-unknown": func(j map[string]any) { j["messages"].([]any)[0].(map[string]any)["extra"] = true }, "message-null": func(j map[string]any) { j["messages"].([]any)[0].(map[string]any)["content"] = nil }}
	for name, mut := range jobCases {
		mut := mut
		cases[name] = func(m map[string]any) {
			for _, j := range m["jobs"].(map[string]any) {
				mut(j.(map[string]any))
			}
		}
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			s, _ := openTestStore(t)
			j := mustSubmit(t, s, "fixture")
			authenticatedFixture(t, s, mut)
			if _, e := s.Get("client.a", j.ID); e == nil || e.Error() != "dot_queue_corrupt" {
				t.Fatalf("invalid authenticated state accepted: %v", e)
			}
		})
	}
}
func TestReviewMarkerAndAuthentication(t *testing.T) {
	for _, kind := range []string{"content", "version", "directory", "hardlink", "missing-state"} {
		t.Run(kind, func(t *testing.T) {
			s, dir := openTestStore(t)
			j := mustSubmit(t, s, "marker")
			path := filepath.Join(dir, markerName)
			switch kind {
			case "content":
				os.WriteFile(path, []byte("garbage"), 0600)
			case "version":
				os.WriteFile(path, []byte("2\n"), 0600)
			case "directory":
				os.Remove(path)
				os.Mkdir(path, 0700)
			case "hardlink":
				if e := os.Link(path, filepath.Join(dir, "alias")); e != nil {
					t.Fatal(e)
				}
			case "missing-state":
				os.Remove(filepath.Join(dir, stateFileName))
				if e := s.Initialize(true); e == nil {
					t.Error("missing state reinitialized")
				}
			}
			if _, e := s.Get("client.a", j.ID); e == nil {
				t.Error("invalid marker accepted")
			}
		})
	}
	s, dir := openTestStore(t)
	j := mustSubmit(t, s, "auth")
	b, _ := os.ReadFile(filepath.Join(dir, stateFileName))
	b[len(b)-1] ^= 1
	os.WriteFile(filepath.Join(dir, stateFileName), b, 0600)
	if _, e := s.Get("client.a", j.ID); e == nil {
		t.Error("tampered ciphertext accepted")
	}
}

// Child processes use only temporary fixture state and a fixed, non-secret test key.
func TestQueueProcessHelper(t *testing.T) {
	dir := os.Getenv("DOTQUEUE_TEST_DIR")
	if dir == "" {
		return
	}
	s := reopen(t, dir)
	switch os.Getenv("DOTQUEUE_TEST_OP") {
	case "submit":
		j, e := s.Submit("client.a", sampleSubmission("process-same"))
		if e != nil {
			t.Fatal(e)
		}
		fmt.Println("JOB=" + j.ID)
	case "claim":
		_, e := s.Claim("client.a", os.Getenv("DOTQUEUE_TEST_JOB"), os.Getenv("DOTQUEUE_TEST_WORKER"))
		if e != nil {
			fmt.Println("CLAIM=" + e.Error())
		} else {
			fmt.Println("CLAIM=ok")
		}
	}
}
func TestReviewCrossProcessCAS(t *testing.T) {
	s, dir := openTestStore(t)
	run := func(op, job string) []string {
		outs := make([]string, 2)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				cmd := exec.Command(os.Args[0], "-test.run=^TestQueueProcessHelper$", "-test.v")
				cmd.Env = append(os.Environ(), "DOTQUEUE_TEST_DIR="+dir, "DOTQUEUE_TEST_OP="+op, "DOTQUEUE_TEST_JOB="+job, fmt.Sprintf("DOTQUEUE_TEST_WORKER=worker.%d", i))
				<-start
				b, e := cmd.CombinedOutput()
				if e != nil {
					t.Errorf("child: %v %s", e, b)
				}
				outs[i] = string(b)
			}(i)
		}
		close(start)
		wg.Wait()
		return outs
	}
	submits := run("submit", "")
	j, e := s.ByRequest("client.a", "process-same")
	if e != nil {
		t.Fatal(e)
	}
	for _, out := range submits {
		if !strings.Contains(out, "JOB="+j.ID+"\n") && !strings.Contains(out, "JOB="+j.ID+"\r\n") {
			t.Fatalf("child returned another job: %s", out)
		}
	}
	ns, e := s.Pending("client.a")
	if e != nil || len(ns) != 1 {
		t.Fatalf("duplicate job/notice %v %v", ns, e)
	}
	ns2, _ := reopen(t, dir).Pending("client.a")
	if !reflect.DeepEqual(ns, ns2) {
		t.Error("notice changed on restart")
	}
	outs := run("claim", j.ID)
	all := strings.Join(outs, "\n")
	if strings.Count(all, "CLAIM=ok") != 1 || strings.Count(all, "CLAIM=dot_queue_claim_conflict") != 1 {
		t.Fatalf("competing claim: %s", all)
	}
}
