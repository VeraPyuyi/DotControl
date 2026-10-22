package dotbridge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/dotcontrol/dotcontrol/internal/dotqueue"
)

type listInterceptQueue struct {
	QueueAPI
	calls *atomic.Int32
	after func()
}

func (q listInterceptQueue) List(id string) ([]dotqueue.Job, error) {
	q.calls.Add(1)
	jobs, err := q.QueueAPI.List(id)
	if q.after != nil {
		q.after()
	}
	return jobs, err
}
func TestListAPIOwnerMetadata(t *testing.T) {
	s, token, id := openTestBridge(t)
	pair, _, err := s.Pair("client.b")
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(s.Handler())
	defer api.Close()
	code, raw := listHTTPRequest(t, api, "GET", "/dot-bridge/v1/jobs", token, nil)
	if code != 200 || !bytes.Contains(raw, []byte(`"jobs":[]`)) {
		t.Fatalf("empty: %d %s", code, raw)
	}
	owners := []string{id, pair.ClientID}
	tokens := []string{token, pair.Token}
	jobs := make([][]dotqueue.Job, 2)
	var claimToken string
	for n, owner := range owners {
		for i := 0; i < 2; i++ {
			job, err := s.queue.Submit(owner, dotqueue.Submission{RequestID: fmt.Sprintf("counter.%d", i), SessionID: fmt.Sprintf("session.%d", n), Mode: dotqueue.ModeAnswer, Messages: []dotqueue.Message{{Role: "user", Content: fmt.Sprintf("private-prompt-%d", n)}}})
			if err != nil {
				t.Fatal(err)
			}
			jobs[n] = append(jobs[n], job)
		}
		claim, err := s.queue.Claim(owner, jobs[n][0].ID, "worker")
		if err != nil {
			t.Fatal(err)
		}
		claimToken = claim.Token
		if _, err = s.queue.Complete(jobs[n][0].ID, claim.Token, dotqueue.Result{Text: strings.Repeat("<", 262144)}); err != nil {
			t.Fatal(err)
		}
	}
	for n := range owners {
		code, raw = listHTTPRequest(t, api, "GET", "/dot-bridge/v1/jobs", tokens[n], nil)
		var env struct {
			Version int      `json:"version"`
			Jobs    []JobDTO `json:"jobs"`
		}
		if code != 200 || json.Unmarshal(raw, &env) != nil || env.Version != 1 || len(env.Jobs) != 2 {
			t.Fatalf("list: %d %s", code, raw)
		}
		if env.Jobs[0].JobID != jobs[n][1].ID || env.Jobs[1].JobID != jobs[n][0].ID {
			t.Fatalf("order: %#v", env.Jobs)
		}
		for _, forbidden := range []string{"private-prompt", claimToken, tokens[0], tokens[1], jobs[1-n][0].ID, jobs[1-n][1].ID, fmt.Sprintf("session.%d", 1-n), `"result"`, `"messages"`, `"task"`, `"token"`} {
			if bytes.Contains(raw, []byte(forbidden)) {
				t.Fatalf("list leaked %q", forbidden)
			}
		}
		if len(raw) > 4096 {
			t.Fatalf("unbounded result in list: %d", len(raw))
		}
		code, raw = listHTTPRequest(t, api, "GET", "/dot-bridge/v1/jobs/"+jobs[n][0].ID, tokens[n], nil)
		var full JobDTO
		if code != 200 || json.Unmarshal(raw, &full) != nil || full.Result == nil || len(full.Result.Text) != 262144 {
			t.Fatalf("full result: status %d", code)
		}
	}
}
func TestListAPIGates(t *testing.T) {
	for _, condition := range []string{"missing_auth", "bad_auth", "revoked", "disabled", "paused", "paused_after", "disabled_after", "revoked_after"} {
		t.Run(condition, func(t *testing.T) {
			var paused atomic.Bool
			s, token, id := openTestBridgeWithPause(t, paused.Load)
			var calls atomic.Int32
			alter := func() {
				switch strings.TrimSuffix(condition, "_after") {
				case "paused":
					paused.Store(true)
				case "disabled":
					s.mu.Lock()
					s.state.Enabled = false
					s.mu.Unlock()
				case "revoked":
					if err := s.RevokeClient(id); err != nil {
						t.Fatal(err)
					}
				}
			}
			var after func()
			if strings.HasSuffix(condition, "_after") {
				after = alter
			} else {
				alter()
			}
			s.queue = listInterceptQueue{QueueAPI: s.queue, calls: &calls, after: after}
			api := httptest.NewServer(s.Handler())
			defer api.Close()
			if condition == "missing_auth" {
				token = ""
			}
			if condition == "bad_auth" {
				token = "wrong"
			}
			code, raw := listHTTPRequest(t, api, "GET", "/dot-bridge/v1/jobs", token, nil)
			if code == 200 || bytes.Contains(raw, []byte(`"jobs"`)) {
				t.Fatalf("gate disclosed list: %d %s", code, raw)
			}
			want := int32(0)
			if after != nil {
				want = 1
			}
			if calls.Load() != want {
				t.Fatalf("queue reads %d want %d", calls.Load(), want)
			}
		})
	}
}
func readyListAPIClient(t *testing.T, s *Service, id string) {
	t.Helper()
	probe, err := s.StartProbe(id)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimTask(id, probe.JobID, "worker.ready")
	if err != nil {
		t.Fatal(err)
	}
	answer := strings.TrimPrefix(claim.Submission.Messages[0].Content, "Reply with exactly this nonce: ")
	if _, err = s.CompleteTask(probe.JobID, claim.Token, dotqueue.Result{Text: answer}); err != nil {
		t.Fatal(err)
	}
}
func TestSubmitEncodedBudget(t *testing.T) {
	for _, character := range []string{"a", "<", "\x01"} {
		t.Run(fmt.Sprintf("byte_%02x", character[0]), func(t *testing.T) {
			// Construct wire bytes independently of the production JSON encoder.
			escaped := character
			if character == "<" {
				escaped = `\u003c`
			}
			if character == "\x01" {
				escaped = `\u0001`
			}
			messages := make([]string, 32)
			for i := range messages {
				messages[i] = `{"role":"user","content":"` + strings.Repeat(escaped, 8192) + `"}`
			}
			wire := `{"request_id":"budget.1","mode":"answer","session_id":"chat","messages":[` + strings.Join(messages, ",") + `]}`
			req, err := decodeSubmit(strings.NewReader(wire))
			if err != nil {
				t.Fatal(err)
			}
			s, token, id := openTestBridge(t)
			readyListAPIClient(t, s, id)
			api := httptest.NewServer(s.Handler())
			defer api.Close()
			code, raw := listHTTPRequest(t, api, "POST", "/dot-bridge/v1/jobs", token, []byte(wire))
			if code != http.StatusAccepted {
				t.Fatalf("full-budget HTTP submission: %d %s", code, raw)
			}
			sub := dotqueue.Submission{RequestID: req.RequestID, Mode: req.Mode, SessionID: req.SessionID}
			for _, m := range req.Messages {
				sub.Messages = append(sub.Messages, dotqueue.Message{Role: m.Role, Content: m.Content})
			}
			if _, err = s.queue.Submit(id, sub); err != nil {
				t.Fatalf("raw queue validation: %v", err)
			}
			sub.RequestID = "over.raw"
			sub.Messages[0].Content += "a"
			if _, err = s.queue.Submit(id, sub); err == nil {
				t.Fatal("queue accepted raw content over 256KiB")
			}
		})
	}
}

type submitCountingQueue struct {
	QueueAPI
	calls *atomic.Int32
	err   error
}

func (q submitCountingQueue) Submit(id string, sub dotqueue.Submission) (dotqueue.Job, error) {
	q.calls.Add(1)
	return dotqueue.Job{}, q.err
}
func TestSubmitWireRejectionsBeforeQueue(t *testing.T) {
	s, token, _ := openTestBridge(t)
	var calls atomic.Int32
	s.queue = submitCountingQueue{QueueAPI: s.queue, calls: &calls}
	api := httptest.NewServer(s.Handler())
	defer api.Close()
	const budget = 2 << 20
	valid := `{"request_id":"counter.1","mode":"answer","messages":[{"role":"user","content":"hi"}]}`
	bodies := []string{valid + strings.Repeat(" ", budget+1-len(valid)), valid + ` {}`, `{"unknown":true}`}
	for _, body := range bodies {
		code, raw := listHTTPRequest(t, api, "POST", "/dot-bridge/v1/jobs", token, []byte(body))
		if code != 400 || !bytes.Contains(raw, []byte("invalid_request")) {
			t.Fatalf("rejection: %d %s", code, raw)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("rejected requests wrote queue %d times", calls.Load())
	}
	if _, err := decodeSubmit(strings.NewReader(valid + strings.Repeat(" ", budget-len(valid)))); err != nil {
		t.Fatalf("exact budget: %v", err)
	}
}
func TestSubmitTypedErrors(t *testing.T) {
	s, token, id := openTestBridge(t)
	readyListAPIClient(t, s, id)
	original := s.queue
	api := httptest.NewServer(s.Handler())
	defer api.Close()
	for _, tc := range []struct {
		code   string
		status int
	}{{"conflict", 409}, {"queue_full", 429}, {"task_probe_required", 400}, {"bridge_paused", 400}} {
		var calls atomic.Int32
		s.queue = submitCountingQueue{QueueAPI: original, calls: &calls, err: errors.New(tc.code)}
		code, raw := listHTTPRequest(t, api, "POST", "/dot-bridge/v1/jobs", token, []byte(`{"request_id":"typed.1","mode":"answer","messages":[{"role":"user","content":"hello"}]}`))
		if code != tc.status || !bytes.Contains(raw, []byte(`"code":"`+tc.code+`"`)) || bytes.Contains(raw, []byte("accepted")) {
			t.Fatalf("%s: %d %s", tc.code, code, raw)
		}
	}
}
