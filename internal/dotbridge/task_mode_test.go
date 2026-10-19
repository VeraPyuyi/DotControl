package dotbridge_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/dotbridge"
	"github.com/dotcontrol/dotcontrol/internal/dotevents"
	"github.com/dotcontrol/dotcontrol/internal/dotqueue"
	"github.com/dotcontrol/dotcontrol/internal/localapi"
	"github.com/dotcontrol/dotcontrol/internal/mcpserver"
)

// These fixtures exercise the authenticated HTTP/MCP workflow, not a cloud computer.
type taskFixture struct {
	s      *dotbridge.Service
	pair   dotbridge.PairingFile
	api    *httptest.Server
	rpc    func(string, map[string]any) json.RawMessage
	tool   func(string, any) json.RawMessage
	paused atomic.Bool
}

func newTaskFixture(t *testing.T) *taskFixture {
	t.Helper()
	f := &taskFixture{}
	callback := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		_ = json.NewDecoder(r.Body).Decode(&p)
		if challenge, ok := p["challenge"].(string); ok {
			_ = json.NewEncoder(w).Encode(map[string]string{"challenge": challenge})
			return
		}
		w.WriteHeader(200)
	}))
	t.Cleanup(callback.Close)
	tr := callback.Client().Transport.(*http.Transport).Clone()
	tr.Proxy = nil
	tr.TLSClientConfig.ServerName = "example.com"
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, callback.Listener.Addr().String())
	}
	delivery := dotevents.NewDelivery(&http.Client{Transport: tr, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }})
	v := dotbridge.NewFakeVault()
	if err := dotbridge.EnsureStateKey(v); err != nil {
		t.Fatal(err)
	}
	var err error
	f.s, err = dotbridge.Open(context.Background(), dotbridge.Config{Root: filepath.Join(t.TempDir(), "bridge"), Listen: "127.0.0.1:9317", Vault: v, MCPQueue: "device.task-fixture", EventsDelivery: delivery, Paused: f.paused.Load})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.s.Close() })
	if err = f.s.Enable(); err != nil {
		t.Fatal(err)
	}
	f.pair, _, err = f.s.Pair("")
	if err != nil {
		t.Fatal(err)
	}
	h, err := mcpserver.HTTPWithBridge(backend{}, f.s)
	if err != nil {
		t.Fatal(err)
	}
	const hop = "fixture-mcp-credential-32-characters-long"
	server := httptest.NewServer(localapi.Guard(hop, h))
	t.Cleanup(server.Close)
	f.rpc = func(method string, params map[string]any) json.RawMessage {
		t.Helper()
		params["_meta"] = map[string]any{"io.modelcontextprotocol/protocolVersion": "2026-07-28", "io.modelcontextprotocol/clientCapabilities": map[string]any{}, "io.modelcontextprotocol/clientInfo": map[string]any{"name": "fixture", "version": "1"}}
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		req, _ := http.NewRequest("POST", server.URL, bytes.NewReader(b))
		req.Header.Set("X-DotControl-Token", hop)
		req.Header.Set("Mcp-Method", method)
		req.Header.Set("MCP-Protocol-Version", "2026-07-28")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if method == "tools/call" {
			req.Header.Set("Mcp-Name", params["name"].(string))
		}
		res, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		var env struct {
			Result json.RawMessage
			Error  json.RawMessage
		}
		if json.Unmarshal(raw, &env) != nil || res.StatusCode != 200 || len(env.Error) > 0 {
			t.Fatalf("%s: %d %s", method, res.StatusCode, raw)
		}
		return env.Result
	}
	f.tool = func(name string, args any) json.RawMessage {
		t.Helper()
		raw := f.rpc("tools/call", map[string]any{"name": name, "arguments": args})
		var result struct {
			IsError           bool
			StructuredContent json.RawMessage
		}
		if json.Unmarshal(raw, &result) != nil || result.IsError {
			t.Fatalf("%s: %s", name, raw)
		}
		return result.StructuredContent
	}
	f.api = httptest.NewServer(f.s.Handler())
	t.Cleanup(f.api.Close)
	f.subscribe(t)
	return f
}
func (f *taskFixture) subscribe(t *testing.T) {
	t.Helper()
	f.rpc("events/subscribe", map[string]any{"name": dotevents.EventName, "arguments": map[string]any{"queue_id": f.pair.QueueID}, "delivery": map[string]any{"mode": "webhook", "url": "https://callback.example/hook", "secret": "whsec_" + base64.StdEncoding.EncodeToString([]byte("01234567890123456789012345678901"))}})
}
func (f *taskFixture) request(t *testing.T, method, path string, body any) (int, []byte) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, f.api.URL+path, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+f.pair.Token)
	res, err := f.api.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, raw
}
func (f *taskFixture) status(t *testing.T, text, tasks bool) {
	t.Helper()
	code, raw := f.request(t, "GET", "/dot-bridge/v1/status", nil)
	var s dotbridge.StatusDTO
	if json.Unmarshal(raw, &s) != nil || code != 200 || s.Capabilities.Text != text || s.Capabilities.Tasks != tasks || s.Ready != (text || tasks) {
		t.Fatalf("status want text=%v tasks=%v: %d %s", text, tasks, code, raw)
	}
}
func (f *taskFixture) claim(t *testing.T, id string) dotqueue.Claim {
	t.Helper()
	var c dotqueue.Claim
	if err := json.Unmarshal(f.tool("dot_task_claim", map[string]any{"queue_id": f.pair.QueueID, "job_id": id, "worker_id": "fixture." + id}), &c); err != nil {
		t.Fatal(err)
	}
	return c
}
func (f *taskFixture) probe(t *testing.T, mode string) (dotqueue.Claim, string) {
	t.Helper()
	// A dynamic interface lets RED report the missing API as a behavioral failure.
	starter, ok := any(f.s).(interface {
		StartProbeMode(string, string) (dotbridge.ProbeResult, error)
	})
	if !ok {
		t.Fatal("missing mode-specific probe API")
	}
	p, err := starter.StartProbeMode(f.pair.ClientID, mode)
	if err != nil {
		t.Fatal(err)
	}
	c := f.claim(t, p.JobID)
	if c.Submission.Mode != mode {
		t.Fatalf("probe mode %q", c.Submission.Mode)
	}
	if mode == dotqueue.ModeAnswer {
		return c, strings.TrimPrefix(c.Submission.Messages[0].Content, "Reply with exactly this nonce: ")
	}
	// Task prompt must request the reversible cloud operation and contain one nonce.
	for _, word := range []string{"cloud workspace", "temporary file", "write", "read", "delete"} {
		if !strings.Contains(c.Submission.Task, word) {
			t.Fatalf("task prompt missing %q: %s", word, c.Submission.Task)
		}
	}
	fields := strings.Fields(c.Submission.Task)
	return c, fields[len(fields)-1]
}
func (f *taskFixture) complete(t *testing.T, c dotqueue.Claim, r dotqueue.Result) {
	t.Helper()
	f.tool("dot_task_complete", map[string]any{"job_id": c.Job.ID, "token": c.Token, "result": r})
}
func TestTaskModeHTTPIndependentProofs(t *testing.T) {
	f := newTaskFixture(t)
	f.status(t, false, false)
	task := map[string]any{"request_id": "normal.task", "session_id": "session", "mode": "task", "task": "fixture background work"}
	code, raw := f.request(t, "POST", "/dot-bridge/v1/jobs", task)
	if code == 202 {
		t.Fatalf("task admitted without proof: %s", raw)
	}
	code, _ = f.request(t, "GET", "/dot-bridge/v1/requests/normal.task", nil)
	if code == 200 {
		t.Fatal("denied task persisted")
	}
	a, nonce := f.probe(t, "answer")
	f.complete(t, a, dotqueue.Result{Text: nonce})
	f.status(t, true, false)
	code, raw = f.request(t, "POST", "/dot-bridge/v1/jobs", task)
	if code == 202 {
		t.Fatalf("answer enabled task: %s", raw)
	}
	wrong, _ := f.probe(t, "task")
	f.status(t, true, false)
	f.complete(t, wrong, dotqueue.Result{Text: nonce})
	f.status(t, true, false)
	c, taskNonce := f.probe(t, "task")
	f.complete(t, c, dotqueue.Result{Text: taskNonce})
	f.status(t, true, true)
	code, raw = f.request(t, "POST", "/dot-bridge/v1/jobs", task)
	if code != 202 {
		t.Fatalf("task not accepted: %d %s", code, raw)
	}
	var job dotbridge.JobDTO
	_ = json.Unmarshal(raw, &job)
	c = f.claim(t, job.JobID)
	f.complete(t, c, dotqueue.Result{Text: "fixture result", Artifacts: []dotqueue.Artifact{{URL: "https://example.com/result", Label: "fixture artifact"}}})
	code, raw = f.request(t, "GET", "/dot-bridge/v1/jobs/"+job.JobID, nil)
	var completed dotbridge.JobDTO
	if code != 200 || json.Unmarshal(raw, &completed) != nil {
		t.Fatalf("task result: %d %s", code, raw)
	}
	if completed.Version != 1 || completed.JobID != job.JobID || completed.RequestID != job.RequestID || completed.RequestDigest != job.RequestDigest || completed.SessionID != job.SessionID || completed.Mode != "task" || completed.State != "completed" || completed.Result == nil {
		t.Fatalf("completed result identity/state mismatch: %#v", completed)
	}
	result := completed.Result
	if result.Text != "fixture result" || result.ErrorCode != "" || result.Cancelled || len(result.Artifacts) != 1 || result.Artifacts[0].URL != "https://example.com/result" || result.Artifacts[0].Label != "fixture artifact" {
		t.Fatalf("completed result payload mismatch: %#v", result)
	}
	// A queued cancellation is terminal; a claimed cancellation remains intent
	// until the consumer explicitly acknowledges it.
	for _, claimed := range []bool{false, true} {
		task["request_id"] = fmt.Sprintf("cancel.%v", claimed)
		code, raw = f.request(t, "POST", "/dot-bridge/v1/jobs", task)
		if code != 202 {
			t.Fatalf("cancel submit: %d %s", code, raw)
		}
		if err := json.Unmarshal(raw, &job); err != nil {
			t.Fatal(err)
		}
		if claimed {
			c = f.claim(t, job.JobID)
		}
		code, raw = f.request(t, "POST", "/dot-bridge/v1/jobs/"+job.JobID+"/cancel", nil)
		want := "cancelled"
		if claimed {
			want = "cancel_requested"
		}
		if code != 200 || !bytes.Contains(raw, []byte(`"state":"`+want+`"`)) {
			t.Fatalf("cancel state: %d %s", code, raw)
		}
		if claimed {
			f.complete(t, c, dotqueue.Result{Cancelled: true})
			code, raw = f.request(t, "GET", "/dot-bridge/v1/jobs/"+job.JobID, nil)
			if code != 200 || !bytes.Contains(raw, []byte(`"state":"cancelled"`)) {
				t.Fatalf("cancel acknowledgement: %d %s", code, raw)
			}
		}
	}
	a, nonce = f.probe(t, "answer")
	f.status(t, false, true)
	f.complete(t, a, dotqueue.Result{Text: nonce})
	f.status(t, true, true)
	f.paused.Store(true)
	f.status(t, false, false)
	f.paused.Store(false)
	f.subscribe(t)
	f.status(t, false, false)
}
func TestTaskModeHTTPUnsuccessfulProbes(t *testing.T) {
	for _, outcome := range []string{"cancelled", "failed", "wrong_nonce", "subscription_rotated"} {
		t.Run(outcome, func(t *testing.T) {
			f := newTaskFixture(t)
			c, nonce := f.probe(t, "task")
			r := dotqueue.Result{Text: nonce}
			switch outcome {
			case "cancelled":
				f.request(t, "POST", "/dot-bridge/v1/jobs/"+c.Job.ID+"/cancel", nil)
				r.Cancelled = true
			case "failed":
				r.ErrorCode = "fixture_failed"
			case "wrong_nonce":
				r.Text = "wrong"
			case "subscription_rotated":
				f.subscribe(t)
			}
			f.complete(t, c, r)
			f.status(t, false, false)
		})
	}
}
