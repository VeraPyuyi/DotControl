package mcpserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dotcontrol/dotcontrol/internal/dotevents"
)

// rawMCPPost sends a JSON-RPC request without requiring a 200 result, so a
// protocol error body can be asserted.
func rawMCPPost(t *testing.T, url, method string, params any) (int, map[string]json.RawMessage) {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Method", method)
	req.Header.Set("MCP-Protocol-Version", "2026-07-28")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var env map[string]json.RawMessage
	if err := json.NewDecoder(res.Body).Decode(&env); err != nil {
		t.Fatalf("%s: undecodable body: %v", method, err)
	}
	return res.StatusCode, env
}

// A failed callback verification must surface the documented -32015 code with a
// classified data.reason instead of an opaque error.
func TestEventsSubscribeCallbackFailureIsClassified(t *testing.T) {
	s := openBridgeForMCPTest(t)
	// No fixture transport: the default public transport rejects this callback.
	s.ReplaceEventsDeliveryForTest(dotevents.NewDelivery(nil))
	h, err := HTTPWithBridge(bridgeNoopBackend{}, s)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	_, env := rawMCPPost(t, server.URL, "events/subscribe", map[string]any{
		"_meta":     eventsMeta()["_meta"],
		"name":      "dot.task.available",
		"arguments": map[string]string{"queue_id": "client.a"},
		"delivery": map[string]string{
			"mode":   "webhook",
			"url":    "https://127.0.0.1/hook",
			"secret": "whsec_MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
		},
		"cursor": nil,
	})
	raw, ok := env["error"]
	if !ok {
		t.Fatalf("expected a JSON-RPC error, got %s", env["result"])
	}
	var e struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &e) != nil {
		t.Fatalf("undecodable error: %s", raw)
	}
	if e.Code != -32015 {
		t.Fatalf("code = %d, want -32015: %s", e.Code, raw)
	}
	var data struct {
		Reason string `json:"reason"`
	}
	if json.Unmarshal(e.Data, &data) != nil || data.Reason == "" {
		t.Fatalf("missing classified reason: %s", raw)
	}
}
