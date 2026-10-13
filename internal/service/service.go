// Package service gives local MCP and paired gateways the same execution path.
package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/dotcontrol/dotcontrol/internal/codexbridge"
	"github.com/dotcontrol/dotcontrol/internal/codexhistory"
	"github.com/dotcontrol/dotcontrol/internal/engine"
	"github.com/dotcontrol/dotcontrol/internal/model"
)

type Request struct {
	HistoryList  *codexhistory.ListRequest `json:"history_list,omitempty"`
	HistoryRead  *codexhistory.ReadRequest `json:"history_read,omitempty"`
	DeviceID     string                    `json:"device_id,omitempty"`
	TaskID       string                    `json:"task_id,omitempty"`
	Target       model.Target              `json:"target,omitempty"`
	Action       *model.Action             `json:"action,omitempty"`
	Codex        *model.CodexSubmit        `json:"codex,omitempty"`
	JobID        string                    `json:"job_id,omitempty"`
	Continuation *model.CodexContinue      `json:"continuation,omitempty"`
}
type Backend interface {
	Call(context.Context, string, Request) (any, error)
}
type Local struct {
	History *codexhistory.Service
	Engine  *engine.Engine
	Bridge  *codexbridge.Bridge
	// BaseStatus can report driver transport readiness without probing desktop
	// content. Unknown is the safe default; activity alone is not readiness.
	BaseStatus func() model.Capability
}

func (s Local) Call(ctx context.Context, method string, r Request) (any, error) {
	device := s.Engine.Device()
	if method == "devices" {
		device.Capabilities = codexbridge.DisabledCapabilities()
		if s.Bridge != nil {
			device.Capabilities = s.Bridge.Capabilities()
			device.CodexProfiles = s.Bridge.ProfileCatalog()
		}
		if s.BaseStatus != nil {
			device.Capabilities.Base = s.BaseStatus()
		}
		device.Capabilities.CodexHistory = model.HistoryCapability{State: "disabled", Code: "history_disabled"}
		if s.History != nil {
			device.Capabilities.CodexHistory = s.History.Capability()
		}
		if s.Bridge != nil && s.Bridge.TerminalOnly() {
			device.Capabilities.Base = model.Capability{State: "not_required", Code: "terminal_only"}
		} else if device.Paused {
			device.Capabilities.Base = model.Capability{State: "paused", Code: "device_paused"}
		} else if !device.Activity.Available {
			device.Capabilities.Base = model.Capability{State: "unavailable", Code: "activity_unavailable"}
		} else if device.Activity.Locked {
			device.Capabilities.Base = model.Capability{State: "blocked", Code: "device_locked"}
		}
		return []model.Device{device}, nil
	}
	if r.DeviceID != device.ID {
		return nil, errors.New("wrong_device")
	}
	if s.Bridge != nil && s.Bridge.TerminalOnly() && (method == "apps" || method == "observe" || method == "execute") {
		return nil, errors.New("terminal_only")
	}
	switch method {
	case "codex_projects", "codex_threads", "codex_read_thread":
		if s.History == nil {
			return nil, errors.New("history_disabled")
		}
		if method == "codex_read_thread" {
			if r.HistoryRead == nil {
				return nil, errors.New("history_invalid_request")
			}
			return s.History.Read(ctx, *r.HistoryRead)
		}
		if r.HistoryList == nil {
			return nil, errors.New("history_invalid_request")
		}
		if method == "codex_projects" {
			return s.History.Projects(ctx, *r.HistoryList)
		}
		return s.History.Threads(ctx, *r.HistoryList)
	case "codex_submit", "codex_job", "codex_cancel", "codex_continue":
		if s.Bridge == nil {
			if method == "codex_submit" && r.Codex != nil && (r.Codex.RetiredFieldsPresent || r.Codex.ServerProfileID != "" || r.Codex.ExperimentID != "") {
				return nil, errors.New("feature_retired")
			}
			return model.CodexUnsupported{DeviceID: device.ID, State: "unsupported", Code: "codex_bridge_not_configured"}, nil
		}
		switch method {
		case "codex_submit":
			if r.Codex == nil || r.Codex.DeviceID != r.DeviceID {
				return nil, errors.New("wrong_device")
			}
			return s.Bridge.Submit(ctx, *r.Codex)
		case "codex_job":
			return s.Bridge.Job(r.DeviceID, r.JobID)
		case "codex_continue":
			if r.Continuation == nil || r.Continuation.DeviceID != r.DeviceID {
				return nil, errors.New("wrong_device")
			}
			return s.Bridge.Continue(ctx, *r.Continuation)
		default:
			return s.Bridge.Cancel(r.DeviceID, r.JobID)
		}
	case "apps":
		return s.Engine.Apps(ctx)
	case "observe":
		return s.Engine.Observe(ctx, r.Target)
	case "execute":
		if r.Action == nil || r.Action.DeviceID != r.DeviceID {
			return nil, errors.New("wrong_device")
		}
		return s.Engine.Execute(ctx, *r.Action)
	case "task":
		return s.Engine.Task(r.TaskID)
	case "cancel":
		if err := s.Engine.Cancel(r.TaskID); err != nil {
			return nil, err
		}
		return map[string]any{"cancelled": true, "task_id": r.TaskID}, nil
	default:
		return nil, errors.New("unknown_method")
	}
}

// Decode rejects unrecognized fields on the trusted transport boundary too.
func Decode(data []byte, out any) error {
	var raw json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return errors.New("invalid_json")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return errors.New("invalid_request")
	}
	if dec.Decode(new(any)) != io.EOF {
		return errors.New("invalid_request")
	}
	return nil
}
