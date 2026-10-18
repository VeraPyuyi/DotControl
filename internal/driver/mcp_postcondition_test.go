package driver

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/engine"
	"github.com/dotcontrol/dotcontrol/internal/mcpserver"
	"github.com/dotcontrol/dotcontrol/internal/model"
	"github.com/dotcontrol/dotcontrol/internal/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type postconditionMonitor struct{}

func (postconditionMonitor) Snapshot() model.Activity {
	return model.Activity{Available: true, IdleMillis: 30000}
}

// Exercise the public MCP boundary, local service, durable engine and real Cua
// verifier. Only native input/window reads are replaced with synthetic fixtures.
func TestMCPExecuteStructuredPostconditionsEndToEnd(t *testing.T) {
	for _, transport := range []string{"memory", "http"} {
		for _, tc := range []struct{ name, observed, want string }{
			{"matched", "new", "succeeded"},
			{"unmatched", "old", "unknown"},
		} {
			t.Run(transport+"/"+tc.name, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				var inputs atomic.Int32
				f := &fakeCaller{respond: func(name string, _ map[string]any) (*mcp.CallToolResult, error) {
					switch name {
					case "list_windows":
						return &mcp.CallToolResult{StructuredContent: map[string]any{"windows": []any{map[string]any{"pid": 100, "window_id": 200, "bounds": map[string]any{"x": 0, "y": 0}}}}}, nil
					case "get_window_state":
						value := "old"
						if inputs.Load() > 0 {
							value = tc.observed
						}
						return &mcp.CallToolResult{StructuredContent: map[string]any{"elements": []any{map[string]any{"element_token": "field", "value": value}}}, Content: []mcp.Content{&mcp.ImageContent{MIMEType: "image/png", Data: []byte("synthetic image")}}}, nil
					case "click":
						inputs.Add(1)
						return &mcp.CallToolResult{StructuredContent: map[string]any{"effect": "confirmed"}}, nil
					default:
						t.Errorf("unexpected native call: %s", name)
						return &mcp.CallToolResult{IsError: true}, nil
					}
				}}
				eng, err := engine.New(engine.Config{DeviceID: "device-test", StateDir: t.TempDir()}, &Cua{client: f}, postconditionMonitor{})
				if err != nil {
					t.Fatal(err)
				}
				defer eng.Close()
				backend := service.Local{Engine: eng}
				var ct mcp.Transport
				if transport == "http" {
					srv := httptest.NewServer(mcpserver.HTTP(backend))
					defer srv.Close()
					ct = &mcp.StreamableClientTransport{Endpoint: srv.URL}
				} else {
					st, mem := mcp.NewInMemoryTransports()
					ss, err := mcpserver.New(backend).Connect(ctx, st, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer ss.Close()
					ct = mem
				}
				client := mcp.NewClient(&mcp.Implementation{Name: "postcondition-test", Version: "1"}, nil)
				cs, err := client.Connect(ctx, ct, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer cs.Close()
				call := func(name string, args any, out any) {
					t.Helper()
					result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
					if err != nil {
						t.Fatal(err)
					}
					if result.IsError {
						t.Fatalf("%s failed: %+v", name, result.Content)
					}
					raw, err := json.Marshal(result.StructuredContent)
					if err != nil || json.Unmarshal(raw, out) != nil {
						t.Fatalf("invalid %s result: %s (%v)", name, raw, err)
					}
				}
				var observation model.Observation
				call("observe", map[string]any{"device_id": "device-test", "target": model.Target{PID: 100, WindowID: "200"}}, &observation)
				conditions := []model.Postcondition{{Kind: "changed", Element: "field", Property: "value", Expected: "new", MaxWaitMillis: 100}}
				args := map[string]any{"device_id": "device-test", "task_id": "task-test", "action_id": "action-test", "observation_id": observation.ID, "target": observation.View.Target, "kind": "click", "args": map[string]any{"element_token": "field"}, "mode": "background", "postconditions": conditions}
				var receipt, retry model.Receipt
				call("execute", args, &receipt)
				if receipt.State != tc.want {
					t.Fatalf("structured postcondition not enforced: %+v, want %s", receipt, tc.want)
				}
				call("execute", args, &retry)
				if retry != receipt || inputs.Load() != 1 {
					t.Fatalf("retry changed receipt or repeated input: %+v %+v inputs=%d", receipt, retry, inputs.Load())
				}
				conditions[0].Expected = "different"
				result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "execute", Arguments: args})
				if err != nil || !result.IsError || len(result.Content) != 1 {
					t.Fatalf("changed postcondition did not conflict: %+v %v", result, err)
				}
				text, ok := result.Content[0].(*mcp.TextContent)
				if !ok || !strings.Contains(text.Text, "action_conflict") || inputs.Load() != 1 {
					t.Fatalf("postcondition not bound to durable request: %+v", result.Content)
				}
			})
		}
	}
}
