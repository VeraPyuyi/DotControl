// Package service gives local MCP and paired gateways the same execution path.
package service

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/dotcontrol/dotcontrol/internal/engine"
	"github.com/dotcontrol/dotcontrol/internal/model"
)

type Request struct {
	DeviceID string        `json:"device_id,omitempty"`
	TaskID   string        `json:"task_id,omitempty"`
	Target   model.Target  `json:"target,omitempty"`
	Action   *model.Action `json:"action,omitempty"`
}
type Backend interface {
	Call(context.Context, string, Request) (any, error)
}
type Local struct{ Engine *engine.Engine }

func (s Local) Call(ctx context.Context, method string, r Request) (any, error) {
	device := s.Engine.Device()
	if method == "devices" {
		return []model.Device{device}, nil
	}
	if r.DeviceID != device.ID {
		return nil, errors.New("wrong_device")
	}
	switch method {
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
