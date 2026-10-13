package codexbridge

import (
	"encoding/json"
	"time"
)

// Pending requests stop the dedicated owned turn; no answer is ever synthesized.
func (b *Bridge) interruptPendingRequests(id string, c *Client) {
	pending := c.PendingRequests()
	b.mu.Lock()
	defer b.mu.Unlock()
	j := b.runtime.jobs[id]
	if j == nil || j.client != c || runtimeTerminal(j.entry.Job.State) {
		return
	}
	for _, request := range pending {
		var params struct {
			ThreadID string `json:"threadId"`
			TurnID   string `json:"turnId"`
		}
		parseErr := json.Unmarshal(request.Params, &params)
		{
			// This client is dedicated to the owned run. A request that cannot
			// bind safely must stop that run rather than wait indefinitely or
			// receive an approval. Explicit foreign bindings remain untouched.
			if parseErr == nil && ((j.entry.Job.ThreadID != "" && params.ThreadID != "" && params.ThreadID != j.entry.Job.ThreadID) || (j.entry.Job.TurnID != "" && params.TurnID != "" && params.TurnID != j.entry.Job.TurnID)) {
				continue
			}
			if !j.entry.NeedsLocalSetup {
				j.entry.NeedsLocalSetup = true
				j.entry.Job.CancelRequested = true
				j.entry.Job.Code = "needs_local_setup"
				j.entry.Job.Message = runtimeMessage("needs_local_setup")
				j.entry.Job.UpdatedAt = time.Now().UTC()
				j.entry.Job.Runtime.Approvals = nil
				_ = b.saveRuntimeLocked(j)
				if j.cancel != nil {
					j.cancel()
				}
			}
			return
		}
	}
}

// Only a dispatched turn/start may use a pending request as its turn binding.
// The dedicated client and already durable thread must agree, and conflicting
// turn IDs remain unknown. This never authorizes a response or another start.
func (b *Bridge) bindPendingStartupTurn(id string, c *Client) bool {
	pending := c.PendingRequests()
	b.mu.Lock()
	defer b.mu.Unlock()
	j := b.runtime.jobs[id]
	if j == nil || j.client != c || !j.entry.NeedsLocalSetup || j.entry.Phase != "turn_intent" || j.entry.Job.ThreadID == "" || j.entry.Job.TurnID != "" {
		return false
	}
	turn := ""
	for _, request := range pending {
		var params struct{ ThreadID, TurnID string }
		if json.Unmarshal(request.Params, &params) != nil || params.ThreadID != j.entry.Job.ThreadID || params.TurnID == "" {
			continue
		}
		if turn != "" && turn != params.TurnID {
			return false
		}
		turn = params.TurnID
	}
	if turn == "" {
		return false
	}
	j.entry.Job.TurnID = turn
	return b.saveRuntimeLocked(j) == nil
}
