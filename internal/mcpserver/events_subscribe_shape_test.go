package mcpserver

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestEventsSubscribeOfficialResponseShapeOverHTTP(t *testing.T) {
	s := openBridgeForMCPTest(t)
	delivery, closeDelivery := NewTestEventsDelivery(t)
	defer closeDelivery()
	s.ReplaceEventsDeliveryForTest(delivery)
	h, err := HTTPWithBridge(bridgeNoopBackend{}, s)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	env := eventsMCPRequest(t, server.URL, "events/subscribe", map[string]any{
		"_meta": map[string]any{
			"io.modelcontextprotocol/protocolVersion":    "2026-07-28",
			"io.modelcontextprotocol/clientCapabilities": map[string]any{},
			"io.modelcontextprotocol/clientInfo":         map[string]any{"name": "fixture", "version": "1"},
		},
		"name":      "dot.task.available",
		"arguments": map[string]string{"queue_id": "client.a"},
		"delivery": map[string]string{
			"mode":   "webhook",
			"url":    fixtureCallbackURL,
			"secret": "whsec_MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
		},
		"cursor": nil,
	})
	var out map[string]json.RawMessage
	if json.Unmarshal(env["result"], &out) != nil {
		t.Fatalf("subscribe envelope: %s", env["result"])
	}
	if out["id"] == nil || string(out["id"]) == `""` {
		t.Fatalf("id missing: %s", env["result"])
	}
	var refresh string
	if json.Unmarshal(out["refreshBefore"], &refresh) != nil || refresh == "" {
		t.Fatalf("refreshBefore missing: %s", env["result"])
	}
	if string(out["cursor"]) != "null" || string(out["truncated"]) != "false" {
		t.Fatalf("official cursor/truncated: %s", env["result"])
	}
	if _, nested := out["subscription"]; nested {
		t.Fatalf("nested subscription object: %s", env["result"])
	}
	if _, leaked := out["secret"]; leaked {
		t.Fatalf("secret leaked: %s", env["result"])
	}
}
