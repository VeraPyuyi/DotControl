package mcpserver

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/model"
	"github.com/dotcontrol/dotcontrol/internal/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Approval answers are transport-level human input, never tool arguments.
type reviewArgs struct {
	DeviceID   string `json:"device_id"`
	JobID      string `json:"job_id"`
	ApprovalID string `json:"approval_id"`
}
type reviewChallenge struct {
	Args    reviewArgs
	Digest  [32]byte
	Expires time.Time
}
type reviewBroker struct {
	mu         sync.Mutex
	challenges map[string]reviewChallenge
}

var errInputRequired = errors.New("human_input_required")

func addReviewTool(s *mcp.Server, b service.Backend) {
	broker := &reviewBroker{challenges: map[string]reviewChallenge{}}
	mcp.AddTool(s, &mcp.Tool{Name: "codex_review", Description: "Open a real human confirmation for one pending target Codex approval. Supply only the returned device/job/approval IDs, never an accept decision. The host asks the person via MCP elicitation. If unavailable, the target remains waiting. Requests are bound to their original thread and turn and expire; cancelling the control job does not stop a server experiment.", Annotations: &mcp.ToolAnnotations{IdempotentHint: true}}, func(ctx context.Context, req *mcp.CallToolRequest, a reviewArgs) (*mcp.CallToolResult, any, error) {
		return broker.review(ctx, req, a, b)
	})
}

func (b *reviewBroker) review(ctx context.Context, req *mcp.CallToolRequest, a reviewArgs, backend service.Backend) (*mcp.CallToolResult, any, error) {
	envelope := service.Request{DeviceID: a.DeviceID, JobID: a.JobID, ApprovalID: a.ApprovalID}
	caps := req.ClientCapabilities()
	if caps == nil || caps.Elicitation == nil || (caps.Elicitation.Form == nil && caps.Elicitation.URL != nil) {
		return call(ctx, backend, "codex_review", envelope)
	}
	var challenge reviewChallenge
	var response *mcp.ElicitResult
	if req.Params.RequestState != "" || len(req.Params.InputResponses) > 0 {
		b.mu.Lock()
		var ok bool
		challenge, ok = b.challenges[req.Params.RequestState]
		delete(b.challenges, req.Params.RequestState)
		b.mu.Unlock()
		if !ok || challenge.Args != a || !time.Now().Before(challenge.Expires) {
			return nil, nil, errors.New("review_state_expired_or_mismatched")
		}
		var valid bool
		response, valid = req.Params.InputResponses["human_review"].(*mcp.ElicitResult)
		if !valid || response == nil || len(req.Params.InputResponses) != 1 {
			return nil, nil, errors.New("invalid_human_review_response")
		}
		if response.Action != "accept" && response.Action != "decline" && response.Action != "cancel" {
			return nil, nil, errors.New("invalid_human_review_response")
		}
	}
	var prompt *model.ReviewPrompt
	envelope.Reviewer = func(_ context.Context, p model.ReviewPrompt) (model.ReviewAnswer, error) {
		encoded, _ := json.Marshal(p)
		digest := sha256.Sum256(encoded)
		if response != nil {
			if challenge.Digest != digest {
				return model.ReviewAnswer{}, errors.New("approval_changed")
			}
			return model.ReviewAnswer{Action: response.Action, Content: response.Content}, nil
		}
		prompt = &p
		return model.ReviewAnswer{}, errInputRequired
	}
	value, err := backend.Call(ctx, "codex_review", envelope)
	if prompt == nil {
		if err != nil {
			return nil, nil, err
		}
		return formatResult("codex_review", value)
	}
	if prompt.DeviceID != a.DeviceID || prompt.JobID != a.JobID || prompt.ApprovalID != a.ApprovalID {
		return nil, nil, errors.New("approval_binding_mismatch")
	}
	expires := prompt.ExpiresAt
	if expires.IsZero() || !time.Now().Before(expires) {
		return nil, nil, errors.New("approval_expired")
	}
	if max := time.Now().Add(5 * time.Minute); expires.After(max) {
		expires = max
	}
	data := make([]byte, 32)
	if _, err = rand.Read(data); err != nil {
		return nil, nil, errors.New("review_state_unavailable")
	}
	token := hex.EncodeToString(data)
	encoded, _ := json.Marshal(prompt)
	b.mu.Lock()
	for k, c := range b.challenges {
		if time.Now().After(c.Expires) || c.Args == a {
			delete(b.challenges, k)
		}
	}
	if len(b.challenges) >= 256 {
		b.mu.Unlock()
		return nil, nil, errors.New("too_many_pending_reviews")
	}
	b.challenges[token] = reviewChallenge{Args: a, Digest: sha256.Sum256(encoded), Expires: expires}
	b.mu.Unlock()
	message := fmt.Sprintf("Target device: %s\nJob: %s\nThread: %s\nTurn: %s\nOperation: %s\nScope: %s\n%s\n%s", pSafe(prompt.DeviceID), pSafe(prompt.JobID), pSafe(prompt.ThreadID), pSafe(prompt.TurnID), prompt.Operation, prompt.Scope, prompt.Message, prompt.Details)
	return &mcp.CallToolResult{InputRequests: mcp.InputRequestMap{"human_review": &mcp.ElicitParams{Mode: "form", Message: message, RequestedSchema: prompt.Schema}}, RequestState: token}, nil, nil
}

// IDs originate in authenticated state, but keep the human header one line.
func pSafe(s string) string {
	for _, r := range s {
		if r < ' ' {
			return "invalid_identifier"
		}
	}
	return s
}
