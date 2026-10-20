package dotevents

import (
	"encoding/json"
	"time"
)

const EventName = "dot.task.available"

type Arguments struct {
	QueueID string `json:"queue_id"`
}

type Callback struct {
	Mode   string `json:"mode"`
	URL    string `json:"url"`
	Secret string `json:"secret"`
}

type Params struct {
	Name      string          `json:"name"`
	Arguments Arguments       `json:"arguments"`
	Delivery  Callback        `json:"delivery"`
	Cursor    json.RawMessage `json:"cursor,omitempty"`
	TTL       json.RawMessage `json:"ttlMs,omitempty"`
}

type Subscription struct {
	ID               string    `json:"id"`
	ExpiresAt        time.Time `json:"expiresAt"`
	CallbackVerified bool      `json:"callbackVerified"`
}

type Definition struct {
	Name          string         `json:"name"`
	Description   string         `json:"description"`
	Delivery      []string       `json:"delivery"`
	InputSchema   map[string]any `json:"inputSchema"`
	PayloadSchema map[string]any `json:"payloadSchema"`
}

type Notice struct {
	OccurredAt time.Time
	EventID    string
	QueueID    string
	JobID      string
	Mode       string
}

type Status struct {
	Subscribed       bool   `json:"subscribed"`
	CallbackVerified bool   `json:"callback_verified"`
	DotReady         bool   `json:"dot_ready"`
	Attempts         int    `json:"attempts"`
	Acknowledged     bool   `json:"acknowledged"`
	LastCode         string `json:"last_code,omitempty"`
	// SubscribeFailure explains the most recent refused subscription once it
	// is no longer active. It carries no secret or callback URL.
	SubscribeFailure *SubscribeFailure `json:"subscribe_failure,omitempty"`
}

// SubscribeFailure is bounded, secret-free diagnostics for the last refused
// events/subscribe attempt.
type SubscribeFailure struct {
	At     string `json:"at"`
	Reason string `json:"reason"`
	Stage  string `json:"stage"`
	Rule   string `json:"rule,omitempty"`
}
