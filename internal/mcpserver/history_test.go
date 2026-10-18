package mcpserver

import (
	"context"
	"encoding/json"
	"github.com/dotcontrol/dotcontrol/internal/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"strings"
	"testing"
)

func TestHistoryToolsReadOnlyAndPreserveResult(t *testing.T) {
	for _, transport := range []string{"memory", "http"} {
		t.Run(transport, func(t *testing.T) {
			ctx, cs := connect(t, backendFunc(func(_ context.Context, m string, r service.Request) (any, error) {
				if r.DeviceID != "device-a" {
					t.Fatal(r)
				}
				if m == "codex_read_thread" {
					if r.HistoryRead == nil || r.HistoryRead.Limit != 5 {
						t.Fatal(r)
					}
				} else if r.HistoryList == nil {
					t.Fatal(r)
				}
				return map[string]any{"revision": 3, "history_marker": "kept", "turns": []any{}}, nil
			}), transport)
			catalog, e := cs.ListTools(ctx, nil)
			if e != nil {
				t.Fatal(e)
			}
			for _, tool := range catalog.Tools {
				if tool.Name == "codex_projects" || tool.Name == "codex_threads" || tool.Name == "codex_read_thread" {
					if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
						t.Fatal("not read-only")
					}
				}
			}
			for _, method := range []string{"codex_projects", "codex_threads", "codex_read_thread"} {
				args := map[string]any{"device_id": "device-a", "limit": 5}
				if method == "codex_read_thread" {
					args["thread_id"] = "11111111-1111-4111-8111-111111111111"
				}
				r, e := cs.CallTool(ctx, &mcp.CallToolParams{Name: method, Arguments: args})
				if e != nil || r.IsError {
					t.Fatalf("%v %v", r, e)
				}
				b, _ := json.Marshal(r)
				if !strings.Contains(string(b), "history_marker") {
					t.Fatal(string(b))
				}
			}
		})
	}
}
