package dotbridge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/dotcontrol/dotcontrol/internal/dotqueue"
)

const exactBudget = 2 << 20

func listHTTPRequest(t *testing.T, api *httptest.Server, method, path, token string, body []byte) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, api.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := api.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, raw
}

// budgetPayload builds a valid submit object padded with legal JSON whitespace
// to an exact encoded size. Content bytes are constructed directly, never
// serialized by the production encoder.
func budgetPayload(t *testing.T, requestID, content string, size int) []byte {
	t.Helper()
	head := `{"request_id":"` + requestID + `","mode":"answer","session_id":"chat","messages":[{"role":"user","content":"` + content + `"}]}`
	body := []byte(head)
	if len(body) >= size {
		t.Fatalf("payload %d exceeds budget %d", len(body), size)
	}
	body = append(body, []byte(strings.Repeat(" ", size-len(body)))...)
	if len(body) != size {
		t.Fatalf("built %d want %d", len(body), size)
	}
	if _, err := decodeSubmit(bytes.NewReader(body)); err != nil {
		t.Fatalf("constructed body invalid: %v", err)
	}
	return body
}

func countOwnerJobs(t *testing.T, s *Service, clientID string) int {
	t.Helper()
	jobs, err := s.queue.List(clientID)
	if err != nil {
		t.Fatal(err)
	}
	return len(jobs)
}

func readyHTTPClient(t *testing.T, s *Service, id string) {
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

// persistedContent returns total stored raw prompt bytes through a real claim,
// the only public path exposing submission content.
func persistedContent(t *testing.T, s *Service, queueID, jobID, worker string) int {
	t.Helper()
	claim, err := s.queue.Claim(queueID, jobID, worker)
	if err != nil {
		t.Fatalf("claim persisted job: %v", err)
	}
	total := 0
	for _, m := range claim.Submission.Messages {
		total += len(m.Content)
	}
	return total
}

func submitReceipt(t *testing.T, api *httptest.Server, token string, body []byte) (int, []byte, JobDTO) {
	t.Helper()
	code, raw := listHTTPRequest(t, api, "POST", "/dot-bridge/v1/jobs", token, body)
	var dto JobDTO
	if code == http.StatusAccepted && json.Unmarshal(raw, &dto) != nil {
		t.Fatalf("receipt: %s", raw)
	}
	return code, raw, dto
}

// TestExactBudgetThroughRealHandler submits an exact 2 MiB body and the next
// byte through the real handler against a queue-reachable ready client.
func TestExactBudgetThroughRealHandler(t *testing.T) {
	s, token, id := openTestBridge(t)
	readyHTTPClient(t, s, id)
	api := httptest.NewServer(s.Handler())
	defer api.Close()
	code, raw, accepted := submitReceipt(t, api, token, []byte(`{"request_id":"control.1","mode":"answer","session_id":"chat","messages":[{"role":"user","content":"hello"}]}`))
	if code != http.StatusAccepted || !accepted.Accepted {
		t.Fatalf("control: %d %s", code, raw)
	}
	if countOwnerJobs(t, s, id) != 2 {
		t.Fatalf("control job not persisted: %d", countOwnerJobs(t, s, id))
	}
	exact := budgetPayload(t, "budget.exact", strings.Repeat("a", 256*1024), exactBudget)
	code, raw, accepted = submitReceipt(t, api, token, exact)
	if code != http.StatusAccepted || !accepted.Accepted {
		t.Fatalf("exact 2MiB: %d %s", code, raw)
	}
	if countOwnerJobs(t, s, id) != 3 {
		t.Fatalf("exact budget job not persisted: %d", countOwnerJobs(t, s, id))
	}
	if got := persistedContent(t, s, id, accepted.JobID, "worker.audit.exact"); got != 256*1024 {
		t.Fatalf("persisted raw content %d", got)
	}
	over := append(append([]byte(nil), exact...), ' ')
	code, raw, _ = submitReceipt(t, api, token, over)
	if code != http.StatusBadRequest || !bytes.Contains(raw, []byte(`"code":"invalid_request"`)) {
		t.Fatalf("oversize: %d %s", code, raw)
	}
	if countOwnerJobs(t, s, id) != 3 {
		t.Fatalf("oversize wrote a queue job: %d", countOwnerJobs(t, s, id))
	}
}

// TestWorstEscapingBudgetThroughRealHandler proves the full 256 KiB raw content
// budget survives the encoder's six-byte escapes.
func TestWorstEscapingBudgetThroughRealHandler(t *testing.T) {
	for _, tc := range []struct{ escaped, raw string }{{`\u003c`, "<"}, {`\u0001`, "\x01"}} {
		t.Run(fmt.Sprintf("escape_%s", tc.escaped), func(t *testing.T) {
			s, token, id := openTestBridge(t)
			readyHTTPClient(t, s, id)
			api := httptest.NewServer(s.Handler())
			defer api.Close()
			// 32 messages of fully escaped bytes totalling exactly 256 KiB raw content.
			var msgs []string
			for i := 0; i < 32; i++ {
				msgs = append(msgs, `{"role":"user","content":"`+strings.Repeat(tc.escaped, 8192)+`"}`)
			}
			wire := []byte(`{"request_id":"budget.escape","mode":"answer","session_id":"chat","messages":[` + strings.Join(msgs, ",") + `]}`)
			if len(wire) > exactBudget {
				t.Fatalf("constructed wire %d exceeds budget", len(wire))
			}
			code, raw, accepted := submitReceipt(t, api, token, wire)
			if code != http.StatusAccepted || !accepted.Accepted {
				t.Fatalf("worst escaping: %d %s", code, raw)
			}
			if got := persistedContent(t, s, id, accepted.JobID, "worker.audit.escape"); got != 256*1024 {
				t.Fatalf("raw content %d", got)
			}
		})
	}
}

// writeAfterAccessQueue persists the real submission, then flips service access
// state. The write is real; the state change happens only after it.
type writeAfterAccessQueue struct {
	QueueAPI
	mutate func()
	once   sync.Once
}

func (q *writeAfterAccessQueue) Submit(clientID string, sub dotqueue.Submission) (dotqueue.Job, error) {
	job, err := q.QueueAPI.Submit(clientID, sub)
	if err == nil {
		q.once.Do(q.mutate)
	}
	return job, err
}

// TestWriteAfterPauseStaysConservative proves a durable write followed by a
// pause is never reported accepted and the original job survives unchanged.
func TestWriteAfterPauseStaysConservative(t *testing.T) {
	s, token, id := openTestBridge(t)
	readyHTTPClient(t, s, id)
	api := httptest.NewServer(s.Handler())
	defer api.Close()
	var paused atomic.Bool
	s.paused = paused.Load
	s.queue = &writeAfterAccessQueue{QueueAPI: s.queue, mutate: func() { paused.Store(true) }}
	code, raw, _ := submitReceipt(t, api, token, []byte(`{"request_id":"write.pause","mode":"answer","session_id":"chat","messages":[{"role":"user","content":"hello"}]}`))
	if code == http.StatusAccepted || bytes.Contains(raw, []byte("accepted")) {
		t.Fatalf("accepted after write: %d %s", code, raw)
	}
	var env ErrorDTO
	if json.Unmarshal(raw, &env) != nil || env.Error.Code != "bridge_paused" {
		t.Fatalf("envelope: %s", raw)
	}
	if len(env.Error.JobID) != 0 {
		t.Fatalf("job id disclosed on rejection: %s", raw)
	}
	jobs, err := s.queue.List(id)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	same := 0
	written := dotqueue.Job{}
	for _, j := range jobs {
		if j.RequestID == "write.pause" {
			same++
			written = j
		}
	}
	if same != 1 {
		t.Fatalf("same-ID jobs %d", same)
	}
	// The write happened before the pause: the job is persisted and still queued.
	if written.State != dotqueue.StateQueued || written.Result != nil {
		t.Fatalf("original job not persisted as written: %#v", written)
	}
	paused.Store(false)
	if code, raw, resumed := submitReceipt(t, api, token, []byte(`{"request_id":"write.pause","mode":"answer","session_id":"chat","messages":[{"role":"user","content":"hello"}]}`)); code != http.StatusAccepted {
		t.Fatalf("same ID after resume: %d %s", code, raw)
	} else if resumed.JobID != written.ID {
		t.Fatalf("same ID resolved a different job: %q != %q", resumed.JobID, written.ID)
	}
	if countOwnerJobs(t, s, id) != len(jobs) {
		t.Fatalf("job count changed by retry")
	}
	if got := persistedContent(t, s, id, written.ID, "worker.audit.write"); got != len("hello") {
		t.Fatalf("original job altered: %d bytes", got)
	}
}

// TestListErrorPrivacyAndGatePrecedence covers failed list I/O: the gate wins,
// no queue counts, prompts or secrets leak, unknown errors stay opaque.
func TestListErrorPrivacyAndGatePrecedence(t *testing.T) {
	const secret = "vault-secret-material"
	for _, condition := range []string{"gate_after", "gate_before"} {
		t.Run(condition, func(t *testing.T) {
			s, token, id := openTestBridge(t)
			if _, err := s.queue.Submit(id, dotqueue.Submission{RequestID: "leak.probe", Mode: dotqueue.ModeAnswer, Messages: []dotqueue.Message{{Role: "user", Content: "private prompt"}}}); err != nil {
				t.Fatal(err)
			}
			var paused atomic.Bool
			s.paused = paused.Load
			s.queue = &listErrorQueue{QueueAPI: s.queue, secret: secret, mutate: func() { paused.Store(true) }}
			if condition == "gate_before" {
				paused.Store(true)
			}
			api := httptest.NewServer(s.Handler())
			defer api.Close()
			code, raw := listHTTPRequest(t, api, "GET", "/dot-bridge/v1/jobs", token, nil)
			var env ErrorDTO
			if code != http.StatusBadRequest || json.Unmarshal(raw, &env) != nil || env.Error.Code != "bridge_paused" {
				t.Fatalf("gate precedence: %d %s", code, raw)
			}
			for _, forbidden := range []string{secret, "private prompt", "messages", "count", "remaining", `"jobs"`} {
				if bytes.Contains(raw, []byte(forbidden)) {
					t.Fatalf("leaked %q: %s", forbidden, raw)
				}
			}
		})
	}
	t.Run("unknown_error", func(t *testing.T) {
		s, token, _ := openTestBridge(t)
		s.queue = &listErrorQueue{QueueAPI: s.queue, secret: secret}
		api := httptest.NewServer(s.Handler())
		defer api.Close()
		code, raw := listHTTPRequest(t, api, "GET", "/dot-bridge/v1/jobs", token, nil)
		var env ErrorDTO
		if code != http.StatusBadRequest || json.Unmarshal(raw, &env) != nil || env.Error.Code != "unknown" || bytes.Contains(raw, []byte(secret)) {
			t.Fatalf("unknown error disclosed: %s", raw)
		}
	})
}

type listErrorQueue struct {
	QueueAPI
	secret string
	mutate func()
}

func (q *listErrorQueue) List(clientID string) ([]dotqueue.Job, error) {
	jobs, err := q.QueueAPI.List(clientID)
	if q.mutate != nil {
		q.mutate()
	}
	if err != nil {
		return jobs, err
	}
	return nil, errors.New("store failure: " + q.secret)
}
