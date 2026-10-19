package dotbridge

import "time"

const (
	APIVersion    = 1
	StateKeyName  = "dot-bridge-state-key"
	probeValidity = 10 * time.Minute
)

type ClientPolicy struct {
	ClientID      string    `json:"client_id"`
	QueueID       string    `json:"queue_id"`
	TokenHash     string    `json:"token_hash"`
	CreatedAt     time.Time `json:"created_at"`
	Revoked       bool      `json:"revoked"`
	Capabilities  CapPolicy `json:"capabilities"`
	ProbeJobID    string    `json:"probe_job_id,omitempty"`
	ProbeAnswer   string    `json:"probe_answer_hash,omitempty"`
	ProbeVerified time.Time `json:"probe_verified_at,omitempty"`
	ProbeExpires  time.Time `json:"probe_expires_at,omitempty"`
}

type CapPolicy struct {
	Text  bool `json:"text"`
	Tasks bool `json:"tasks"`
}

type PersistedState struct {
	Version int                     `json:"version"`
	Enabled bool                    `json:"enabled"`
	Clients map[string]ClientPolicy `json:"clients"`
}

type StatusDTO struct {
	Version      int             `json:"version"`
	Enabled      bool            `json:"enabled"`
	Paired       bool            `json:"paired"`
	Ready        bool            `json:"ready"`
	Code         string          `json:"code"`
	VerifiedAt   *time.Time      `json:"verified_at,omitempty"`
	ExpiresAt    *time.Time      `json:"expires_at,omitempty"`
	Subscription SubscriptionDTO `json:"subscription"`
	Capabilities CapPolicy       `json:"capabilities"`
}

type SubscriptionDTO struct {
	State     string     `json:"state"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type JobDTO struct {
	Version         int        `json:"version"`
	JobID           string     `json:"job_id"`
	RequestID       string     `json:"request_id"`
	RequestDigest   string     `json:"request_digest"`
	Mode            string     `json:"mode"`
	SessionID       string     `json:"session_id"`
	State           string     `json:"state"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	CancelRequested bool       `json:"cancel_requested"`
	Result          *ResultDTO `json:"result,omitempty"`
	ErrorCode       string     `json:"error_code,omitempty"`
	Accepted        bool       `json:"accepted,omitempty"`
}

type ResultDTO struct {
	Text      string `json:"text"`
	Cancelled bool   `json:"cancelled,omitempty"`
	ErrorCode string `json:"error_code,omitempty"`
}

type SubmitRequest struct {
	RequestID string       `json:"request_id"`
	SessionID string       `json:"session_id"`
	Mode      string       `json:"mode"`
	Messages  []MessageDTO `json:"messages,omitempty"`
	Task      string       `json:"task,omitempty"`
}

type MessageDTO struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ErrorDTO struct {
	Error ErrorBody `json:"error"`
}

type ErrorBody struct {
	Code  string `json:"code"`
	JobID string `json:"job_id,omitempty"`
}

type PairingFile struct {
	Version   int       `json:"version"`
	ClientID  string    `json:"client_id"`
	QueueID   string    `json:"queue_id"`
	Token     string    `json:"token"`
	Listen    string    `json:"listen"`
	CreatedAt time.Time `json:"created_at"`
}
