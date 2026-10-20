package dotbridge

import "time"

const (
	APIVersion    = 1
	StateKeyName  = "dot-bridge-state-key"
	probeValidity = 10 * time.Minute
)

type ClientPolicy struct {
	ClientID                 string     `json:"client_id"`
	QueueID                  string     `json:"queue_id"`
	TokenHash                string     `json:"token_hash"`
	CreatedAt                time.Time  `json:"created_at"`
	Revoked                  bool       `json:"revoked"`
	Capabilities             CapPolicy  `json:"capabilities"`
	ProbeJobID               string     `json:"probe_job_id,omitempty"`
	ProbeAnswer              string     `json:"probe_answer_hash,omitempty"`
	ProbeVerified            time.Time  `json:"probe_verified_at,omitempty"`
	ProbeExpires             time.Time  `json:"probe_expires_at,omitempty"`
	ProbeGeneration          uint64     `json:"probe_generation,omitempty"`
	ProbeSubscriptionExpires time.Time  `json:"probe_subscription_expires_at,omitempty"`
	ProbeConsumed            bool       `json:"probe_consumed,omitempty"`
	TaskProbe                ProbeProof `json:"task_probe,omitempty"`
}

// ProbeProof is bounded private state for one mode's latest challenge.
// Consumed survives expiry and subscription rotation; retries cannot renew proof.
type ProbeProof struct {
	JobID               string    `json:"job_id,omitempty"`
	AnswerHash          string    `json:"answer_hash,omitempty"`
	Verified            time.Time `json:"verified_at,omitempty"`
	Expires             time.Time `json:"expires_at,omitempty"`
	Generation          uint64    `json:"generation,omitempty"`
	SubscriptionExpires time.Time `json:"subscription_expires_at,omitempty"`
	Consumed            bool      `json:"consumed,omitempty"`
}

func (p ClientPolicy) proof(mode string) ProbeProof {
	if mode == "task" {
		return p.TaskProbe
	}
	return ProbeProof{JobID: p.ProbeJobID, AnswerHash: p.ProbeAnswer, Verified: p.ProbeVerified, Expires: p.ProbeExpires, Generation: p.ProbeGeneration, SubscriptionExpires: p.ProbeSubscriptionExpires, Consumed: p.ProbeConsumed}
}
func (p *ClientPolicy) setProof(mode string, proof ProbeProof) {
	if mode == "task" {
		p.TaskProbe = proof
		return
	}
	p.ProbeJobID, p.ProbeAnswer = proof.JobID, proof.AnswerHash
	p.ProbeVerified, p.ProbeExpires = proof.Verified, proof.Expires
	p.ProbeGeneration, p.ProbeSubscriptionExpires, p.ProbeConsumed = proof.Generation, proof.SubscriptionExpires, proof.Consumed
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

// CapacityDTO reports global retained job counts for operator visibility.
// It intentionally omits client-identifying fields.
type CapacityDTO struct {
	Limit     int `json:"limit"`
	Retained  int `json:"retained"`
	Remaining int `json:"remaining"`
}

type StatusDTO struct {
	Version          int                  `json:"version"`
	Enabled          bool                 `json:"enabled"`
	Paired           bool                 `json:"paired"`
	Ready            bool                 `json:"ready"`
	Code             string               `json:"code"`
	VerifiedAt       *time.Time           `json:"verified_at,omitempty"`
	ExpiresAt        *time.Time           `json:"expires_at,omitempty"`
	Subscription     SubscriptionDTO      `json:"subscription"`
	Capabilities     CapPolicy            `json:"capabilities"`
	Capacity         *CapacityDTO         `json:"capacity,omitempty"`
	SubscribeFailure *SubscribeFailureDTO `json:"subscribe_failure,omitempty"`
}

type SubscriptionDTO struct {
	State     string     `json:"state"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// SubscribeFailureDTO reports the most recent refused subscription attempt
// without exposing secrets or the callback URL.
type SubscribeFailureDTO struct {
	At     string `json:"at"`
	Reason string `json:"reason"`
	Stage  string `json:"stage"`
}

type JobDTO struct {
	Version         int        `json:"version"`
	JobID           string     `json:"job_id"`
	RequestID       string     `json:"request_id"`
	RequestDigest   string     `json:"request_digest"`
	Mode            string     `json:"mode"`
	SessionID       string     `json:"session_id"`
	State           string     `json:"state"`
	Summary         string     `json:"summary,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	CancelRequested bool       `json:"cancel_requested"`
	Result          *ResultDTO `json:"result,omitempty"`
	ErrorCode       string     `json:"error_code,omitempty"`
	Accepted        bool       `json:"accepted,omitempty"`
}

type ResultDTO struct {
	Text      string        `json:"text"`
	Artifacts []ArtifactDTO `json:"artifacts,omitempty"`
	Cancelled bool          `json:"cancelled,omitempty"`
	ErrorCode string        `json:"error_code,omitempty"`
}

type ArtifactDTO struct {
	URL   string `json:"url"`
	Label string `json:"label"`
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
