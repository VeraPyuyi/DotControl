package dotbridge_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
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
	"github.com/dotcontrol/dotcontrol/internal/service"
)

type backend struct{}

func (backend) Call(context.Context, string, service.Request) (any, error) { return nil, nil }
func TestReviewHTTPPairedQueueRoundTrip(t *testing.T) {
	var notices atomic.Int32
	callback := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		_ = json.NewDecoder(r.Body).Decode(&p)
		if challenge, ok := p["challenge"].(string); ok {
			_ = json.NewEncoder(w).Encode(map[string]string{"challenge": challenge})
			return
		}
		notices.Add(1)
		w.WriteHeader(200)
	}))
	defer callback.Close()
	tr := callback.Client().Transport.(*http.Transport).Clone()
	tr.Proxy = nil
	tr.TLSClientConfig.ServerName = "example.com"
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, callback.Listener.Addr().String())
	}
	delivery := dotevents.NewDelivery(&http.Client{Transport: tr, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }})
	v := dotbridge.NewFakeVault()
	if e := dotbridge.EnsureStateKey(v); e != nil {
		t.Fatal(e)
	}
	var paused atomic.Bool
	s, e := dotbridge.Open(context.Background(), dotbridge.Config{Root: filepath.Join(physicalTemp(t), "bridge"), Listen: "127.0.0.1:9317", Vault: v, MCPQueue: "device.distinct", EventsDelivery: delivery, Paused: paused.Load})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.Enable(); e != nil {
		t.Fatal(e)
	}
	pair, _, e := s.Pair("")
	if e != nil {
		t.Fatal(e)
	}
	h, e := mcpserver.HTTPWithBridge(backend{}, s)
	if e != nil {
		t.Fatal(e)
	}
	const hop = "fixture-mcp-credential-32-characters-long"
	mcpServer := httptest.NewServer(localapi.Guard(hop, h))
	defer mcpServer.Close()
	rpc := func(method string, params map[string]any) json.RawMessage {
		t.Helper()
		params["_meta"] = map[string]any{"io.modelcontextprotocol/protocolVersion": "2026-07-28", "io.modelcontextprotocol/clientCapabilities": map[string]any{}, "io.modelcontextprotocol/clientInfo": map[string]any{"name": "fixture", "version": "1"}}
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		req, _ := http.NewRequest("POST", mcpServer.URL, bytes.NewReader(b))
		req.Header.Set("X-DotControl-Token", hop)
		req.Header.Set("Mcp-Method", method)
		req.Header.Set("MCP-Protocol-Version", "2026-07-28")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if method == "tools/call" {
			req.Header.Set("Mcp-Name", params["name"].(string))
		}
		res, e := mcpServer.Client().Do(req)
		if e != nil {
			t.Fatal(e)
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
	tool := func(name string, args any) json.RawMessage {
		t.Helper()
		raw := rpc("tools/call", map[string]any{"name": name, "arguments": args})
		var result struct {
			IsError           bool
			StructuredContent json.RawMessage
		}
		if json.Unmarshal(raw, &result) != nil || result.IsError {
			t.Fatalf("%s: %s", name, raw)
		}
		return result.StructuredContent
	}
	rpc("events/subscribe", map[string]any{"name": dotevents.EventName, "arguments": map[string]any{"queue_id": pair.QueueID}, "delivery": map[string]any{"mode": "webhook", "url": "https://callback.example/hook", "secret": "whsec_" + base64.StdEncoding.EncodeToString([]byte("01234567890123456789012345678901"))}})
	probe, e := s.StartProbe(pair.ClientID)
	if e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(5 * time.Second)
	for notices.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if notices.Load() == 0 {
		t.Fatal("no availability callback")
	}
	var claim dotqueue.Claim
	if e = json.Unmarshal(tool("dot_task_claim", map[string]any{"queue_id": pair.QueueID, "job_id": probe.JobID, "worker_id": "attempt.1"}), &claim); e != nil {
		t.Fatal(e)
	}
	answer := strings.TrimPrefix(claim.Submission.Messages[0].Content, "Reply with exactly this nonce: ")
	tool("dot_task_status", map[string]any{"job_id": probe.JobID, "token": claim.Token})
	tool("dot_task_complete", map[string]any{"job_id": probe.JobID, "token": claim.Token, "result": dotqueue.Result{Text: answer}})
	api := httptest.NewServer(s.Handler())
	defer api.Close()
	request := func(method, path string, body any) (int, []byte) {
		t.Helper()
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, api.URL+path, bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer "+pair.Token)
		res, e := api.Client().Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		return res.StatusCode, raw
	}
	code, b := request("GET", "/dot-bridge/v1/status", nil)
	var status dotbridge.StatusDTO
	_ = json.Unmarshal(b, &status)
	if code != 200 || !status.Ready || !status.Capabilities.Text || status.Capabilities.Tasks {
		t.Fatalf("status: %d %s", code, b)
	}
	code, b = request("POST", "/dot-bridge/v1/jobs", map[string]any{"request_id": "normal.1", "session_id": "chat.1", "mode": "answer", "messages": []any{map[string]any{"role": "user", "content": "hello"}}})
	var job dotbridge.JobDTO
	_ = json.Unmarshal(b, &job)
	if code != 202 {
		t.Fatalf("submit: %d %s", code, b)
	}
	_ = json.Unmarshal(tool("dot_task_claim", map[string]any{"queue_id": pair.QueueID, "job_id": job.JobID, "worker_id": "attempt.2"}), &claim)
	tool("dot_task_complete", map[string]any{"job_id": job.JobID, "token": claim.Token, "result": dotqueue.Result{Text: "fixture answer"}})
	code, b = request("GET", "/dot-bridge/v1/jobs/"+job.JobID, nil)
	if code != 200 || !bytes.Contains(b, []byte("fixture answer")) {
		t.Fatalf("get: %d %s", code, b)
	}
	paused.Store(true)
	_, b = request("GET", "/dot-bridge/v1/status", nil)
	_ = json.Unmarshal(b, &status)
	if status.Ready {
		t.Fatal("ready while paused")
	}
}

// physicalTemp resolves the test's temporary root: macOS temporary directories
// sit under the system /var symlink, which the bridge deliberately rejects as
// an ancestor. Production symlink rejection is unchanged.
func physicalTemp(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
