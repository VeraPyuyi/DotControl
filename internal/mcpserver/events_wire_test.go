package mcpserver

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func eventsMCPRequest(t *testing.T, url, method string, params any) map[string]json.RawMessage {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(string(payload)))
	req.Header.Set("Mcp-Method", method)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2026-07-28")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("%s: %d %s", method, res.StatusCode, raw)
	}
	var env map[string]json.RawMessage
	if json.Unmarshal(raw, &env) != nil || env["error"] != nil {
		t.Fatalf("%s: %s", method, raw)
	}
	return env
}

func TestEventsListPublishesOfficialDefinition(t *testing.T) {
	s := openBridgeForMCPTest(t)
	h, err := HTTPWithBridge(bridgeNoopBackend{}, s)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	env := eventsMCPRequest(t, server.URL, "events/list", map[string]any{"_meta": map[string]any{"io.modelcontextprotocol/protocolVersion": "2026-07-28", "io.modelcontextprotocol/clientCapabilities": map[string]any{}, "io.modelcontextprotocol/clientInfo": map[string]any{"name": "fixture", "version": "1"}}})
	var result struct {
		Events []map[string]json.RawMessage `json:"events"`
	}
	if json.Unmarshal(env["result"], &result) != nil || len(result.Events) != 1 {
		t.Fatalf("list: %s", env["result"])
	}
	event := result.Events[0]
	if string(event["name"]) != `"dot.task.available"` {
		t.Fatalf("name: %s", event["name"])
	}
	var delivery []string
	if json.Unmarshal(event["delivery"], &delivery) != nil || len(delivery) != 1 || delivery[0] != "webhook" {
		t.Fatalf("delivery: %s", event["delivery"])
	}
	for _, key := range []string{"inputSchema", "payloadSchema"} {
		var schema map[string]json.RawMessage
		if json.Unmarshal(event[key], &schema) != nil || schema["type"] == nil || schema["properties"] == nil {
			t.Fatalf("%s: %s", key, event[key])
		}
	}
	var input struct {
		Required   []string `json:"required"`
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
	}
	if json.Unmarshal(event["inputSchema"], &input) != nil || len(input.Required) != 1 || input.Required[0] != "queue_id" || input.Properties["queue_id"].Type != "string" {
		t.Fatalf("inputSchema: %s", event["inputSchema"])
	}
	var payload struct {
		Required   []string `json:"required"`
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
	}
	if json.Unmarshal(event["payloadSchema"], &payload) != nil {
		t.Fatalf("payloadSchema: %s", event["payloadSchema"])
	}
	for _, name := range []string{"queue_id", "job_id", "mode"} {
		if payload.Properties[name].Type != "string" {
			t.Fatalf("payload field %s: %s", name, event["payloadSchema"])
		}
	}
	if strings.Contains(string(event["payloadSchema"]), "prompt") || strings.Contains(string(event["payloadSchema"]), "token") {
		t.Fatalf("payload exposes private fields: %s", event["payloadSchema"])
	}
}
