package mcpserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/model"
	"github.com/dotcontrol/dotcontrol/internal/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestReviewUsesHumanElicitationAcrossHTTP(t *testing.T) {
	for _, action := range []string{"accept", "decline", "cancel"} {
		t.Run(action, func(t *testing.T) {
			var human, answered atomic.Int32
			prompt := model.ReviewPrompt{DeviceID: "device-a", JobID: "job-a", ApprovalID: "approval-a", ThreadID: "thread-a", TurnID: "turn-a", Operation: "read test output", Scope: "once", Schema: map[string]any{"type": "object", "properties": map[string]any{}}, ExpiresAt: time.Now().Add(time.Minute)}
			backend := backendFunc(func(ctx context.Context, method string, r service.Request) (any, error) {
				if method != "codex_review" || r.Reviewer == nil {
					return nil, errors.New("reviewer_missing")
				}
				answer, err := r.Reviewer(ctx, prompt)
				if err != nil {
					return nil, err
				}
				if answer.Action != action {
					t.Errorf("action=%s", answer.Action)
				}
				answered.Add(1)
				return map[string]any{"state": "reviewed"}, nil
			})
			srv := httptest.NewServer(HTTP(backend))
			defer srv.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			client := mcp.NewClient(&mcp.Implementation{Name: "test-human-host", Version: "1"}, &mcp.ClientOptions{ElicitationHandler: func(_ context.Context, r *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
				human.Add(1)
				if !strings.Contains(r.Params.Message, "device-a") || !strings.Contains(r.Params.Message, "turn-a") {
					t.Error("binding not displayed")
				}
				return &mcp.ElicitResult{Action: action, Content: map[string]any{}}, nil
			}})
			cs, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer cs.Close()
			res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "codex_review", Arguments: map[string]any{"device_id": "device-a", "job_id": "job-a", "approval_id": "approval-a"}})
			if err != nil {
				t.Fatal(err)
			}
			if res.IsError {
				t.Fatalf("review failed: %#v", res.Content)
			}
			if human.Load() != 1 || answered.Load() != 1 {
				t.Fatalf("human=%d answered=%d", human.Load(), answered.Load())
			}
		})
	}
}

func TestReviewUnsupportedHostAndModelDecision(t *testing.T) {
	var calls atomic.Int32
	ctx, cs := connect(t, backendFunc(func(_ context.Context, m string, r service.Request) (any, error) {
		calls.Add(1)
		if r.Reviewer != nil {
			t.Error("unadvertised human capability")
		}
		return map[string]any{"state": "waiting_approval"}, nil
	}), "http")
	args := map[string]any{"device_id": "d", "job_id": "j", "approval_id": "a"}
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "codex_review", Arguments: args})
	if err != nil || res.IsError {
		t.Fatalf("unsupported: %v %#v", err, res)
	}
	args["accept"] = true
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "codex_review", Arguments: args})
	if err == nil && !res.IsError {
		t.Fatal("model supplied approval accepted")
	}
	if calls.Load() != 1 {
		t.Fatal("model supplied approval reached backend")
	}
}

func TestCodexJobImagesAreNativeAndNotDuplicated(t *testing.T) {
	data := base64.StdEncoding.EncodeToString([]byte("image transport fixture"))
	ctx, cs := connect(t, backendFunc(func(_ context.Context, m string, _ service.Request) (any, error) {
		return model.CodexJob{JobID: "job", Runtime: &model.CodexRuntimeInfo{Verification: "unverified", Images: []model.Image{{MIMEType: "image/png", Data: data}}}}, nil
	}), "http")
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "codex_job", Arguments: map[string]any{"device_id": "d", "job_id": "job"}})
	if err != nil || res.IsError {
		t.Fatalf("image: %v %#v", err, res)
	}
	if len(res.Content) != 2 {
		t.Fatalf("content count %d", len(res.Content))
	}
	if _, ok := res.Content[1].(*mcp.ImageContent); !ok {
		t.Fatal("missing native image")
	}
	encoded, _ := json.Marshal(res)
	if strings.Count(string(encoded), data) != 1 {
		t.Fatal("image duplicated or lost")
	}
}

func TestReviewChallengeExpiryBindingChangeAndReplay(t *testing.T) {
	for _, scenario := range []string{"expired", "changed_args", "changed_prompt", "replay"} {
		t.Run(scenario, func(t *testing.T) {
			b := &reviewBroker{challenges: map[string]reviewChallenge{}}
			a := reviewArgs{DeviceID: "d", JobID: "j", ApprovalID: "a"}
			p := model.ReviewPrompt{DeviceID: "d", JobID: "j", ApprovalID: "a", ThreadID: "thread", TurnID: "turn", Schema: map[string]any{"type": "object", "properties": map[string]any{}}, ExpiresAt: time.Now().Add(time.Minute)}
			answers := 0
			backend := backendFunc(func(ctx context.Context, _ string, r service.Request) (any, error) {
				if r.Reviewer == nil {
					return nil, errors.New("missing human capability")
				}
				_, err := r.Reviewer(ctx, p)
				if err == nil {
					answers++
				}
				return model.CodexJob{}, err
			})
			req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Meta: mcp.Meta{mcp.MetaKeyClientCapabilities: map[string]any{"elicitation": map[string]any{"form": map[string]any{}}}}}}
			result, _, err := b.review(context.Background(), req, a, backend)
			if err != nil || result == nil {
				t.Fatalf("prepare %v", err)
			}
			req.Params.RequestState = result.RequestState
			req.Params.InputResponses = mcp.InputResponseMap{"human_review": &mcp.ElicitResult{Action: "accept", Content: map[string]any{}}}
			switch scenario {
			case "expired":
				c := b.challenges[result.RequestState]
				c.Expires = time.Now().Add(-time.Second)
				b.challenges[result.RequestState] = c
			case "changed_args":
				a.JobID = "other"
			case "changed_prompt":
				p.TurnID = "other"
			}
			_, _, err = b.review(context.Background(), req, a, backend)
			if scenario == "replay" {
				if err != nil || answers != 1 {
					t.Fatalf("first answer %v %d", err, answers)
				}
				_, _, err = b.review(context.Background(), req, a, backend)
				if err == nil || answers != 1 {
					t.Fatal("review replayed")
				}
			} else if err == nil || answers != 0 {
				t.Fatalf("unsafe review %s", scenario)
			}
		})
	}
}

func TestAllJobReturningMethodsExtractImages(t *testing.T) {
	data := base64.StdEncoding.EncodeToString([]byte("test image"))
	for _, method := range []string{"codex_submit", "codex_continue", "codex_cancel", "codex_review", "codex_job"} {
		res, out, err := formatResult(method, model.CodexJob{Runtime: &model.CodexRuntimeInfo{Images: []model.Image{{MIMEType: "image/png", Data: data}}}})
		if err != nil || res == nil || len(res.Content) != 2 {
			t.Fatalf("%s: %v", method, err)
		}
		encoded, _ := json.Marshal(out)
		if strings.Contains(string(encoded), data) {
			t.Fatalf("%s base64 duplicated", method)
		}
	}
}

func TestCodexGIFDoesNotDiscardTextResult(t *testing.T) {
	data := base64.StdEncoding.EncodeToString([]byte("GIF89a transport fixture"))
	for _, method := range []string{"codex_submit", "codex_continue", "codex_job", "codex_cancel", "codex_review"} {
		t.Run(method, func(t *testing.T) {
			res, out, err := formatResult(method, model.CodexJob{Runtime: &model.CodexRuntimeInfo{Text: "result evidence", Images: []model.Image{{MIMEType: "image/gif", Data: data}}}})
			if err != nil || res == nil || len(res.Content) != 2 {
				t.Fatalf("GIF made job unreadable: %v", err)
			}
			image, ok := res.Content[1].(*mcp.ImageContent)
			if !ok || image.MIMEType != "image/gif" {
				t.Fatal("GIF not returned as native image")
			}
			job, ok := out.(model.CodexJob)
			if !ok || job.Runtime.Text != "result evidence" || len(job.Runtime.Images) != 0 {
				t.Fatal("text evidence lost or image duplicated")
			}
		})
	}
}
