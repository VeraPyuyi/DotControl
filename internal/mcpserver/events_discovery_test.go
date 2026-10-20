package mcpserver

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// Discovery must work for the authenticated MCP consumer even when the caller's
// principal is not itself a paired client queue; authorization belongs to
// subscribe, which receives the queue in its arguments.
func TestEventsListDiscoverableBeforeSubscription(t *testing.T) {
	s := openBridgeForMCPTest(t)
	h, err := HTTPWithBridge(bridgeNoopBackend{}, s)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	env := eventsMCPRequest(t, server.URL, "events/list", eventsMeta())
	var listed struct {
		Events []map[string]json.RawMessage `json:"events"`
	}
	if json.Unmarshal(env["result"], &listed) != nil || len(listed.Events) != 1 {
		t.Fatalf("catalog must be discoverable before subscription: %s", env["result"])
	}
	if string(listed.Events[0]["name"]) != `"dot.task.available"` {
		t.Fatalf("unexpected event: %s", env["result"])
	}
}

func eventsMeta() map[string]any {
	return map[string]any{"_meta": map[string]any{
		"io.modelcontextprotocol/protocolVersion":    "2026-07-28",
		"io.modelcontextprotocol/clientCapabilities": map[string]any{},
		"io.modelcontextprotocol/clientInfo":         map[string]any{"name": "fixture", "version": "1"},
	}}
}
