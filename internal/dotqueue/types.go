package dotqueue

import "time"

const (
	ModeAnswer = "answer"
	ModeTask   = "task"

	StateQueued          = "queued"
	StateClaimed         = "claimed"
	StateRunning         = "running"
	StateWaitingUser     = "waiting_user"
	StateCancelRequested = "cancel_requested"
	StateCancelled       = "cancelled"
	StateCompleted       = "completed"
	StateFailed          = "failed"
	StateOutcomeUnknown  = "outcome_unknown"
)

type Submission struct {
	RequestID string    `json:"request_id"`
	Mode      string    `json:"mode"`
	SessionID string    `json:"session_id"`
	Messages  []Message `json:"messages"`
	Task      string    `json:"task"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Job struct {
	ID              string    `json:"id"`
	RequestID       string    `json:"request_id"`
	RequestDigest   string    `json:"request_digest"`
	ClientID        string    `json:"client_id"`
	QueueID         string    `json:"queue_id"`
	Mode            string    `json:"mode"`
	State           string    `json:"state"`
	SessionID       string    `json:"session_id"`
	CancelRequested bool      `json:"cancel_requested"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	Result          *Result   `json:"result,omitempty"`
	ErrorCode       string    `json:"error_code,omitempty"`
}

type Claim struct {
	Job        Job        `json:"job"`
	Submission Submission `json:"submission"`
	Token      string     `json:"token"`
}

type Result struct {
	Text      string     `json:"text"`
	Artifacts []Artifact `json:"artifacts"`
	ErrorCode string     `json:"error_code"`
	Cancelled bool       `json:"cancelled"`
}

type Artifact struct {
	URL   string `json:"url"`
	Label string `json:"label"`
}

type Notice struct {
	EventID string `json:"event_id"`
	QueueID string `json:"queue_id"`
	JobID   string `json:"job_id"`
	Mode    string `json:"mode"`
}
