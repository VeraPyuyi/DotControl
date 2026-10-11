// Package service gives local MCP and paired gateways the same execution path.
package service

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/dotcontrol/dotcontrol/internal/codexbridge"
	"github.com/dotcontrol/dotcontrol/internal/engine"
	"github.com/dotcontrol/dotcontrol/internal/model"
)

type Request struct {
	DeviceID string             `json:"device_id,omitempty"`
	TaskID   string             `json:"task_id,omitempty"`
	Target   model.Target       `json:"target,omitempty"`
	Action   *model.Action      `json:"action,omitempty"`
	Codex    *model.CodexSubmit `json:"codex,omitempty"`
	JobID    string             `json:"job_id,omitempty"`
}
type Backend interface {
	Call(context.Context, string, Request) (any, error)
}
type Local struct {
	Engine *engine.Engine
	Bridge *codexbridge.Bridge
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
		}
		if s.BaseStatus != nil {
			device.Capabilities.Base = s.BaseStatus()
		}
		if device.Paused {
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
	switch method {
	case "codex_submit", "codex_job", "codex_cancel":
		if s.Bridge == nil {
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
	return json.Unmarshal(raw, out)
}
