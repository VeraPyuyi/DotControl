// Package model is the versioned contract shared by local and gateway entry points.
package model

import (
	"context"
	"encoding/json"
	"time"
)

const Version = "0.2.0-alpha.3-dev"

type Target struct {
	PID      int    `json:"pid"`
	WindowID string `json:"window_id"`
}
type Activity struct {
	Available        bool   `json:"available"`
	Locked           bool   `json:"locked"`
	IdleMillis       int64  `json:"idle_ms"`
	Generation       uint64 `json:"generation"`
	ForegroundPID    int    `json:"foreground_pid"`
	ForegroundWindow string `json:"foreground_window"`
}
type Monitor interface{ Snapshot() Activity }

type Image struct {
	Data     string `json:"data"`
	MIMEType string `json:"mime_type"`
}
type View struct {
	Target      Target   `json:"target"`
	Fingerprint string   `json:"fingerprint"`
	Text        string   `json:"text"`
	Images      []Image  `json:"images,omitempty"`
	CaptureID   string   `json:"capture_id,omitempty"`
	Elements    []string `json:"elements,omitempty"`
}
type Observation struct {
	ID        string    `json:"observation_id"`
	DeviceID  string    `json:"device_id"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	View      View      `json:"view"`
}
type Action struct {
	DeviceID      string          `json:"device_id"`
	TaskID        string          `json:"task_id"`
	ActionID      string          `json:"action_id"`
	ObservationID string          `json:"observation_id"`
	Target        Target          `json:"target"`
	Kind          string          `json:"kind"`
	Args          json.RawMessage `json:"args"`
	// Mode is auto, background, or foreground. Foreground requires local idleness.
	Mode   string `json:"mode"`
	Expect string `json:"expect,omitempty"`
}
type Receipt struct {
	DeviceID  string    `json:"device_id"`
	TaskID    string    `json:"task_id"`
	ActionID  string    `json:"action_id"`
	State     string    `json:"state"`
	Mode      string    `json:"mode"`
	Code      string    `json:"code,omitempty"`
	Message   string    `json:"message,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
	// Hash binds retries without storing typed content or screenshot bytes.
	Hash string `json:"-"`
}
type Outcome struct {
	State   string
	Code    string
	Message string
}
type Driver interface {
	Apps(context.Context) (json.RawMessage, error)
	Observe(context.Context, Target) (View, error)
	// SupportsBackground must be conservative and never cause side effects.
	SupportsBackground(Action, View) bool
	// Check performs read-only validation of the exact target and observation.
	Check(context.Context, View) error
	Execute(context.Context, Action, View, string) Outcome
	// Release only releases input held by this agent; never releases a person's input.
	Release(context.Context) error
}
type Device struct {
	ID            string               `json:"device_id"`
	Name          string               `json:"name"`
	OS            string               `json:"os"`
	Version       string               `json:"version"`
	Paused        bool                 `json:"paused"`
	Activity      Activity             `json:"activity"`
	Nonce         string               `json:"nonce"`
	Capabilities  DeviceCapabilities   `json:"capabilities"`
	CodexProfiles *CodexProfileCatalog `json:"codex_profiles,omitempty"`
}

// Only the target user's deliberately configured choices are discoverable.
// Paths, SSH endpoints, identity files and deployment commands stay local.
type CodexProfileCatalog struct {
	Execution []CodexProfileSummary `json:"execution"`
	Servers   []CodexProfileSummary `json:"servers"`
}
type CodexProfileSummary struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Execution    string `json:"execution,omitempty"`
	Presentation string `json:"presentation,omitempty"`
}

// Capability separates a live connection from acceptance of desktop execution.
// Available is never inferred from an installed skill or tool inventory.
type Capability struct {
	Available bool   `json:"available"`
	State     string `json:"state"`
	Code      string `json:"code,omitempty"`
}
type DeviceCapabilities struct {
	Base                Capability `json:"base"`
	CodexConnection     Capability `json:"codex_connection"`
	OfficialComputerUse Capability `json:"official_computer_use"`
	CodexDelegation     Capability `json:"codex_delegation"`
}
type CodexCapabilities = DeviceCapabilities

type CodexSubmit struct {
	DeviceID        string   `json:"device_id"`
	RequestID       string   `json:"request_id"`
	Prompt          string   `json:"prompt"`
	AllowedApps     []string `json:"allowed_apps"`
	WorkingDirs     []string `json:"working_dirs"`
	ProfileID       string   `json:"profile_id,omitempty"`
	ServerProfileID string   `json:"server_profile_id,omitempty"`
	Presentation    string   `json:"presentation,omitempty"`
}

type CodexContinue struct {
	DeviceID  string `json:"device_id"`
	JobID     string `json:"job_id"`
	RequestID string `json:"request_id"`
	Prompt    string `json:"prompt"`
}

// Legacy CodexJob records contain only fixed status and keyed hashes. Optional
// Runtime carries bounded owned results in memory; the runtime journal strips
// all result text, images, evidence and human-review details before persistence.
type CodexJob struct {
	JobID           string            `json:"job_id"`
	DeviceID        string            `json:"device_id"`
	RequestID       string            `json:"request_id"`
	State           string            `json:"state"`
	Code            string            `json:"code"`
	Message         string            `json:"message"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
	PromptHash      string            `json:"prompt_hash"`
	RequestHash     string            `json:"request_hash"`
	CancelRequested bool              `json:"cancel_requested"`
	ProfileID       string            `json:"profile_id,omitempty"`
	ServerProfileID string            `json:"server_profile_id,omitempty"`
	ThreadID        string            `json:"thread_id,omitempty"`
	TurnID          string            `json:"turn_id,omitempty"`
	Runtime         *CodexRuntimeInfo `json:"runtime,omitempty"`
}

// Runtime payloads are bounded and returned only to the owning caller. They are
// never written to the general job journal; restart explicitly loses results.
type CodexRuntimeInfo struct {
	Execution         string          `json:"execution"`
	Presentation      string          `json:"presentation"`
	PresentationState string          `json:"presentation_state"`
	TurnState         string          `json:"turn_state,omitempty"`
	Verification      string          `json:"verification"`
	ResultStatus      string          `json:"result_status"`
	Text              string          `json:"text,omitempty"`
	Images            []Image         `json:"images,omitempty"`
	Evidence          []CodexEvidence `json:"evidence,omitempty"`
	Approvals         []CodexApproval `json:"approvals,omitempty"`
}

type CodexEvidence struct {
	Kind     string `json:"kind"`
	ItemID   string `json:"item_id"`
	Status   string `json:"status"`
	ExitCode *int   `json:"exit_code,omitempty"`
	Source   string `json:"source,omitempty"`
	Output   string `json:"output,omitempty"`
}

// Approval summaries contain no command, file contents, secret or raw schema.
// Exact operation details are supplied only to a trusted human review callback.
type CodexApproval struct {
	ApprovalID string    `json:"approval_id"`
	Kind       string    `json:"kind"`
	Operation  string    `json:"operation"`
	Scope      string    `json:"scope"`
	State      string    `json:"state"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type ReviewPrompt struct {
	DeviceID   string
	JobID      string
	ApprovalID string
	ThreadID   string
	TurnID     string
	Kind       string
	Operation  string
	Scope      string
	Details    string
	Message    string
	Schema     any
	Choices    []string
	ExpiresAt  time.Time
}

type ReviewAnswer struct {
	Action  string
	Content map[string]any
}

type CodexUnsupported struct {
	DeviceID string `json:"device_id"`
	State    string `json:"state"`
	Code     string `json:"code"`
}
