package mcpserver

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReviewBridgeHTTPToolsAndDiscovery(t *testing.T) {
	s := openBridgeForMCPTest(t)
	h, e := HTTPWithBridge(bridgeNoopBackend{}, s)
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	for _, method := range []string{"tools/list", "server/discover"} {
		req, _ := http.NewRequest("POST", server.URL, bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"`+method+`","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/clientInfo":{"name":"fixture","version":"1"}}}}`))
		req.Header.Set("Mcp-Method", method)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("MCP-Protocol-Version", "2026-07-28")
		res, e := server.Client().Do(req)
		if e != nil {
			t.Fatalf("%s: %v", method, e)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("%s: %d %s", method, res.StatusCode, b)
		}
		var env struct {
			Result struct {
				Tools        []any                      `json:"tools"`
				Capabilities map[string]json.RawMessage `json:"capabilities"`
			}
			Error any
		}
		if e := json.Unmarshal(b, &env); e != nil {
			t.Fatal(e)
		}
		if env.Error != nil {
			t.Fatalf("%s: %s", method, b)
		}
		if method == "tools/list" && len(env.Result.Tools) != 17 {
			t.Fatalf("tools: %s", b)
		}
		if method == "server/discover" && string(env.Result.Capabilities["events"]) != "{}" {
			t.Fatalf("discovery: %s", b)
		}
	}
}
