package codexbridge

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/model"
)

type ProbeConfig struct{ Binary, WorkingDir string }
type ProbeReport struct {
	CheckedAt               time.Time        `json:"checked_at"`
	Code                    string           `json:"code"`
	Connection              model.Capability `json:"connection"`
	OfficialComputerUse     model.Capability `json:"official_computer_use"`
	Delegation              model.Capability `json:"delegation"`
	ComputerUseSkillPresent bool             `json:"computer_use_skill_present"`
	NodeREPLPresent         bool             `json:"node_repl_present"`
	CuaREPLPresent          bool             `json:"cua_repl_present"`
	InventoryComplete       bool             `json:"inventory_complete"`
	PendingLocalRequests    int              `json:"pending_local_requests"`
}

func emptyReport() ProbeReport {
	caps := DisabledCapabilities()
	return ProbeReport{CheckedAt: time.Now().UTC(), Code: "codex_not_probed", Connection: caps.CodexConnection, OfficialComputerUse: caps.OfficialComputerUse, Delegation: caps.CodexDelegation}
}

// Probe only inspects the public handshake, skills, and MCP inventory. It never
// starts a thread/turn, invokes a tool, reads user threads, or answers approvals.
// Inventory false means not listed in this probe, never native-tool absence.
func Probe(ctx context.Context, cfg ProbeConfig) (ProbeReport, error) {
	r := emptyReport()
	c, err := Start(ctx, cfg.Binary, cfg.WorkingDir)
	if err != nil {
		r.Code = err.Error()
		r.Connection = model.Capability{State: "unavailable", Code: r.Code}
		return r, err
	}
	defer c.Close()
	return probe(ctx, c, cfg.WorkingDir)
}
func probe(ctx context.Context, c *Client, cwd string) (ProbeReport, error) {
	r := emptyReport()
	fail := func(err error) (ProbeReport, error) {
		r.Code = err.Error()
		r.PendingLocalRequests = len(c.PendingRequests())
		if !r.Connection.Available {
			r.Connection = model.Capability{State: "unavailable", Code: r.Code}
		}
		var transport *TransportError
		if errors.As(err, &transport) {
			r.Connection = model.Capability{State: "unavailable", Code: r.Code}
		}
		return r, err
	}
	var init json.RawMessage
	if err := c.Call(ctx, "initialize", map[string]any{"clientInfo": map[string]string{"name": "dotcontrol_capability_probe", "version": model.Version}, "capabilities": map[string]bool{"experimentalApi": true}}, &init); err != nil {
		return fail(err)
	}
	if err := c.Notify(ctx, "initialized", map[string]any{}); err != nil {
		return fail(err)
	}
	r.Connection = model.Capability{Available: true, State: "probe_passed"}
	var skills struct {
		Data []struct {
			Skills []struct {
				Name    string `json:"name"`
				Enabled bool   `json:"enabled"`
			} `json:"skills"`
		} `json:"data"`
	}
	if err := c.Call(ctx, "skills/list", map[string]any{"cwds": []string{cwd}}, &skills); err != nil {
		return fail(err)
	}
	for _, group := range skills.Data {
		for _, skill := range group.Skills {
			if skill.Enabled && (skill.Name == "computer-use:computer-use" || skill.Name == "computer-use") {
				r.ComputerUseSkillPresent = true
			}
		}
	}
	cursor := ""
	for page := 0; page < 10; page++ {
		params := map[string]any{"limit": 200}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var result struct {
			Data []struct {
				Name  string                     `json:"name"`
				Tools map[string]json.RawMessage `json:"tools"`
			} `json:"data"`
			NextCursor *string `json:"nextCursor"`
		}
		if err := c.Call(ctx, "mcpServerStatus/list", params, &result); err != nil {
			return fail(err)
		}
		for _, server := range result.Data {
			if _, ok := server.Tools["js"]; ok {
				if server.Name == "node_repl" {
					r.NodeREPLPresent = true
				}
				if server.Name == "cua_repl" {
					r.CuaREPLPresent = true
				}
			}
		}
		if result.NextCursor == nil || *result.NextCursor == "" {
			r.InventoryComplete = true
			break
		}
		if *result.NextCursor == cursor {
			return fail(failure("codex_inventory_cursor_repeated"))
		}
		cursor = *result.NextCursor
	}
	if !r.InventoryComplete {
		return fail(failure("codex_inventory_limit"))
	}
	r.PendingLocalRequests = len(c.PendingRequests())
	r.Code = "inventory_only_native_acceptance_pending"
	return r, nil
}
