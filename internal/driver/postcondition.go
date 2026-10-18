package driver

import (
	"context"
	"encoding/json"
	"github.com/dotcontrol/dotcontrol/internal/model"
	"time"
)

func propertyValue(p map[string]any, element, property string) (string, bool) {
	if property != "value" && property != "label" && property != "role" {
		return "", false
	}
	es, _ := p["elements"].([]any)
	var value string
	found := false
	for _, raw := range es {
		el, ok := raw.(map[string]any)
		if !ok || el["element_token"] != element {
			continue
		}
		if found {
			return "", false
		}
		value, ok = el[property].(string)
		if !ok {
			return "", false
		}
		found = true
	}
	return value, found
}
func matchesPostcondition(c model.Postcondition, before, after map[string]any) bool {
	switch c.Kind {
	case "property", "changed":
		v, ok := propertyValue(after, c.Element, c.Property)
		if !ok || v != c.Expected {
			return false
		}
		if c.Kind == "changed" {
			old, ok := propertyValue(before, c.Element, c.Property)
			return ok && old != v
		}
		return true
	case "task_result":
		evidence, ok := after["task_result"].(map[string]any)
		return ok && evidence["source"] == "application_state" && evidence["value"] == c.Expected
	default:
		return false
	}
}
func (d *Cua) verifyPostconditions(ctx context.Context, a model.Action, v model.View) model.Outcome {
	var before map[string]any
	_ = json.Unmarshal([]byte(v.Text), &before)
	maxWait := 0
	for _, c := range a.Postconditions {
		if c.MaxWaitMillis < 1 || c.MaxWaitMillis > 2000 || len(c.Expected) > 4096 {
			return model.Outcome{State: "unknown", Code: "postcondition_invalid"}
		}
		if c.MaxWaitMillis > maxWait {
			maxWait = c.MaxWaitMillis
		}
	}
	started := time.Now()
	deadline := started.Add(time.Duration(maxWait) * time.Millisecond)
	verifyCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	for {
		if verifyCtx.Err() != nil {
			return model.Outcome{State: "unknown", Code: "postcondition_unverified"}
		}
		args, _ := targetArgs(a.Target)
		args["include_screenshot"] = false
		r, err := d.call(verifyCtx, "get_window_state", args)
		if err != nil || r.IsError {
			return model.Outcome{State: "unknown", Code: "postcondition_unverified"}
		}
		after, err := payload(r)
		if err != nil {
			return model.Outcome{State: "unknown", Code: "postcondition_unverified"}
		}
		matched := true
		for _, c := range a.Postconditions {
			if time.Since(started) > time.Duration(c.MaxWaitMillis)*time.Millisecond || !matchesPostcondition(c, before, after) {
				matched = false
				break
			}
		}
		if matched {
			return model.Outcome{State: "succeeded", Code: "postcondition_verified"}
		}
		select {
		case <-verifyCtx.Done():
			return model.Outcome{State: "unknown", Code: "postcondition_unverified"}
		case <-time.After(5 * time.Millisecond):
		}
	}
}
