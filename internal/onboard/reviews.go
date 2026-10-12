package onboard

import (
	"context"
	"github.com/dotcontrol/dotcontrol/internal/model"
)

// ReviewRequest is a human-input channel available only to the authenticated
// same-origin localhost session. No MCP tool accepts Answer or a decision.
type ReviewRequest struct {
	Operation  string              `json:"operation"`
	JobID      string              `json:"job_id,omitempty"`
	ApprovalID string              `json:"approval_id,omitempty"`
	Answer     *model.ReviewAnswer `json:"answer,omitempty"`
}
type ReviewsBackend interface {
	Reviews(context.Context, ReviewRequest) (any, error)
}
