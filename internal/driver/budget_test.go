package driver

import (
	"context"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"strings"
	"testing"
)

func TestResponseBudgetAndMetrics(t *testing.T) {
	f := &fakeCaller{respond: func(string, map[string]any) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: strings.Repeat("private", 700000)}}}, nil
	}}
	d := &Cua{client: f}
	if _, err := d.call(context.Background(), "get_window_state", map[string]any{}); err == nil {
		t.Fatal("oversized response accepted")
	}
	s := d.Diagnostic()
	if s["response_budget_bytes"] != 4<<20 || s["response_over_budget"] != true || s["call_ms"] == nil {
		t.Fatal(s)
	}
}
