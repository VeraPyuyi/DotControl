package mcpserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dotcontrol/dotcontrol/internal/model"
	"github.com/dotcontrol/dotcontrol/internal/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestCodexToolContractRoundTripsThroughBothTransports(t *testing.T) {
	for _, transport := range []string{"memory", "http"} {
		t.Run(transport, func(t *testing.T) {
			seen := make(chan service.Request, 3)
			ctx, cs := connect(t, backendFunc(func(_ context.Context, method string, r service.Request) (any, error) {
				seen <- r
				switch method {
				case "codex_submit":
					return model.CodexJob{DeviceID: r.DeviceID, JobID: "owned-job", RequestID: r.Codex.RequestID, State: "blocked", Code: "official_cu_acceptance_required"}, nil
				case "codex_job", "codex_cancel":
					return model.CodexJob{DeviceID: r.DeviceID, JobID: r.JobID, State: "blocked"}, nil
				default:
					t.Fatalf("wrong method=%s", method)
					return nil, nil
				}
			}), transport)
			res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "codex_submit", Arguments: map[string]any{"device_id": "device-a", "request_id": "request-stable", "prompt": "你好 private prompt", "experiment_id": "fixture-a", "allowed_apps": []string{"Calculator"}, "working_dirs": []string{t.TempDir()}}})
			if err != nil || res.IsError {
				t.Fatalf("submit=%+v %v", res, err)
			}
			r := <-seen
			if r.Codex == nil || r.Codex.Prompt != "你好 private prompt" || r.Codex.RequestID != "request-stable" || r.Codex.ExperimentID != "fixture-a" || len(r.Codex.WorkingDirs) != 1 {
				t.Fatalf("request=%+v", r)
			}
			data, _ := json.Marshal(res.StructuredContent)
			var j model.CodexJob
			if json.Unmarshal(data, &j) != nil || j.JobID != "owned-job" || j.State != "blocked" {
				t.Fatalf("structured job=%s", data)
			}
			for _, method := range []string{"codex_job", "codex_cancel"} {
				res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: method, Arguments: map[string]any{"device_id": "device-a", "job_id": "owned-job"}})
				if err != nil || res.IsError {
					t.Fatalf("%s=%+v %v", method, res, err)
				}
				r = <-seen
				if r.JobID != "owned-job" || r.DeviceID != "device-a" {
					t.Fatalf("query=%+v", r)
				}
			}
		})
	}
}
