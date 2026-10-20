package mcpserver

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// The events/list result keeps an array even when the bridge is disabled, and a
// revoked paired client is not offered an event catalog it cannot subscribe to.
func TestEventsListGateShapes(t *testing.T) {
	metalist := func(t *testing.T) map[string]any {
		return map[string]any{"_meta": map[string]any{
			"io.modelcontextprotocol/protocolVersion":    "2026-07-28",
			"io.modelcontextprotocol/clientCapabilities": map[string]any{},
			"io.modelcontextprotocol/clientInfo":         map[string]any{"name": "fixture", "version": "1"},
		}}
	}
	s := openBridgeForMCPTest(t)
	h, err := HTTPWithBridge(bridgeNoopBackend{}, s)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	env := eventsMCPRequest(t, server.URL, "events/list", metalist(t))
	var listed struct {
		Events []map[string]json.RawMessage `json:"events"`
	}
	if json.Unmarshal(env["result"], &listed) != nil || len(listed.Events) != 1 {
		t.Fatalf("enabled catalog: %s", env["result"])
	}

	if err := s.Disable(); err != nil {
		t.Fatal(err)
	}
	env = eventsMCPRequest(t, server.URL, "events/list", metalist(t))
	var disabled struct {
		Events json.RawMessage `json:"events"`
	}
	if json.Unmarshal(env["result"], &disabled) != nil {
		t.Fatalf("disabled result: %s", env["result"])
	}
	if string(disabled.Events) != "[]" {
		t.Fatalf("disabled events must be an empty array: %s", disabled.Events)
	}
}

func TestEventsListExcludesRevokedClientCatalog(t *testing.T) {
	s := openBridgeForMCPTest(t)
	if err := s.RevokeClient("client.a"); err != nil {
		t.Fatal(err)
	}
	if got := s.EventsList("client.a"); len(got) != 0 {
		t.Fatalf("revoked client sees catalog: %#v", got)
	}
}
