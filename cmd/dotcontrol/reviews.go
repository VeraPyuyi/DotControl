package main

import (
	"context"
	"errors"
	"github.com/dotcontrol/dotcontrol/internal/model"
	"github.com/dotcontrol/dotcontrol/internal/onboard"
)

// Only onboard's same-origin authenticated local session reaches this method.
// Remote MCP receives approval identifiers but never a model-writable answer.
func (o *runtimeOwner) Reviews(ctx context.Context, r onboard.ReviewRequest) (any, error) {
	o.mu.Lock()
	bridge := o.bridge
	deviceID := o.cfg.DeviceID
	o.mu.Unlock()
	if bridge == nil {
		return nil, errors.New("codex_unavailable")
	}
	switch r.Operation {
	case "list":
		return bridge.LocalJobs(deviceID)
	case "take_over":
		return bridge.TakeOverLocal(ctx, deviceID, r.JobID, r.ApprovalID)
	case "prompt":
		var prompt *model.ReviewPrompt
		_, err := bridge.ReviewLocal(ctx, deviceID, r.JobID, r.ApprovalID, func(_ context.Context, p model.ReviewPrompt) (model.ReviewAnswer, error) {
			prompt = &p
			return model.ReviewAnswer{}, errors.New("human_input_required")
		})
		if err != nil && err.Error() != "human_input_required" {
			return nil, err
		}
		if prompt == nil {
			return nil, errors.New("approval_not_found")
		}
		return prompt, nil
	case "answer":
		if r.Answer == nil {
			return nil, errors.New("human_input_required")
		}
		return bridge.ReviewLocal(ctx, deviceID, r.JobID, r.ApprovalID, func(context.Context, model.ReviewPrompt) (model.ReviewAnswer, error) { return *r.Answer, nil })
	default:
		return nil, errors.New("unknown_review_operation")
	}
}
