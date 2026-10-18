package mcpserver

import (
	"context"
	"encoding/json"
	"github.com/dotcontrol/dotcontrol/internal/codexbridge"
	"github.com/dotcontrol/dotcontrol/internal/model"
	"github.com/dotcontrol/dotcontrol/internal/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"strings"
	"testing"
)

func TestRetiredToolAndFieldsAbsentFromCatalog(t *testing.T) {
	for _, transport := range []string{"memory", "http"} {
		t.Run(transport, func(t *testing.T) {
			ctx, cs := connect(t, backendFunc(func(context.Context, string, service.Request) (any, error) { return nil, nil }), transport)
			list, err := cs.ListTools(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(list.Tools) != 13 {
				t.Fatalf("tool count = %d", len(list.Tools))
			}
			for _, tool := range list.Tools {
				if tool.Name == "codex_review" {
					t.Fatal("review tool still exposed")
				}
				if tool.Name == "codex_submit" {
					raw, _ := json.Marshal(tool.InputSchema)
					if strings.Contains(string(raw), "server_profile_id") || strings.Contains(string(raw), "experiment_id") {
						t.Fatal("retired fields advertised")
					}
				}
			}
			res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "codex_submit", Arguments: map[string]any{"device_id": "device-a", "request_id": "x", "prompt": "p", "unexpected": true}})
			if err == nil && !res.IsError {
				t.Fatal("unknown field silently ignored")
			}
		})
	}
}

func TestMCPRetiredRequestsAndOldIdempotentRecordAcrossTransports(t *testing.T) {
	for _, transport := range []string{"memory", "http"} {
		t.Run(transport, func(t *testing.T) {
			b, err := codexbridge.New(codexbridge.Config{DeviceID: "device-a", StateDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			ctx, cs := connect(t, backendFunc(func(ctx context.Context, method string, r service.Request) (any, error) {
				return b.Submit(ctx, *r.Codex)
			}), transport)
			for _, field := range []string{"server_profile_id", "experiment_id"} {
				for _, value := range []any{"legacy", "", nil} {
					args := map[string]any{"device_id": "device-a", "request_id": "new", "prompt": "task", field: value}
					res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "codex_submit", Arguments: args})
					if err != nil || !res.IsError || len(res.Content) != 1 || res.Content[0].(*mcp.TextContent).Text != "feature_retired" {
						t.Fatalf("retirement result: %+v %v", res, err)
					}
				}
			}
			original := model.CodexSubmit{DeviceID: "device-a", RequestID: "old", Prompt: "task", AllowedApps: []string{"Calculator"}, WorkingDirs: []string{t.TempDir()}}
			old, err := b.Submit(ctx, original)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(original)
			var args map[string]any
			json.Unmarshal(raw, &args)
			args["experiment_id"] = ""
			res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "codex_submit", Arguments: args})
			if err != nil || res.IsError {
				t.Fatalf("old retry blocked: %+v %v", res, err)
			}
			raw, _ = json.Marshal(res.StructuredContent)
			var replay model.CodexJob
			json.Unmarshal(raw, &replay)
			if replay.JobID != old.JobID || replay.RequestHash != old.RequestHash {
				t.Fatal("old record changed")
			}
		})
	}
}
