package codexbridge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/model"
)

const maxResultText = 32768
const maxResultImages = 4
const maxImageBytes = 4 << 20

func terminalTurn(status string) bool {
	return status == "completed" || status == "interrupted" || status == "failed"
}
func (b *Bridge) finishTurn(id, status string) {
	state, code := "completed", "model_turn_completed"
	if status == "interrupted" {
		state, code = "cancelled", "turn_interrupted"
	}
	if status == "failed" {
		state, code = "failed", "turn_failed"
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	j := b.runtime.jobs[id]
	b.latchNativeInterruptionLocked(j)
	if j.handoffRequested && !b.runtimeInterruptedLocked(j) {
		state, code = "awaiting_target", "interrupt_for_local_takeover"
	}
	j.entry.Job.State = state
	j.entry.Job.Code = code
	j.entry.Job.Message = runtimeMessage(code)
	j.entry.Job.UpdatedAt = time.Now().UTC()
	j.entry.Job.Runtime.TurnState = status
	j.entry.Job.Runtime.Verification = "unverified"
	j.entry.Job.Runtime.Approvals = nil
	if j.entry.Job.Runtime.ResultStatus != "unavailable_after_restart" {
		j.entry.Job.Runtime.ResultStatus = "available"
	}
	j.entry.Phase = "turn_terminal"
	j.approvals = map[string]*runtimeApproval{}
	_ = b.saveRuntimeLocked(j)
}
func (b *Bridge) consume(ctx context.Context, id string, c *Client) bool {
	tick := time.NewTicker(40 * time.Millisecond)
	defer tick.Stop()
	cancelled := ctx.Done()
	b.mu.Lock()
	handoff := b.runtime.jobs[id].handoff
	b.mu.Unlock()
	var deadline <-chan time.Time
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		select {
		case <-handoff:
			handoff = nil
			b.mu.Lock()
			j := b.runtime.jobs[id]
			thread, turn := j.entry.Job.ThreadID, j.entry.Job.TurnID
			b.mu.Unlock()
			var ack json.RawMessage
			_ = runtimeCall(c, "turn/interrupt", map[string]any{"threadId": thread, "turnId": turn}, &ack)
			timer = time.NewTimer(3 * time.Second)
			deadline = timer.C
		case <-cancelled:
			cancelled = nil
			b.mu.Lock()
			j := b.runtime.jobs[id]
			thread, turn := j.entry.Job.ThreadID, j.entry.Job.TurnID
			j.entry.Job.CancelRequested = true
			j.entry.Job.Code = "interrupt_requested"
			j.entry.Job.Message = runtimeMessage("interrupt_requested")
			err := b.saveRuntimeLocked(j)
			b.mu.Unlock()
			if err != nil {
				return false
			}
			var ack json.RawMessage
			// RPC acknowledgement is never a stop receipt. Wait for turn/completed below.
			_ = runtimeCall(c, "turn/interrupt", map[string]any{"threadId": thread, "turnId": turn}, &ack)
			if timer == nil {
				timer = time.NewTimer(3 * time.Second)
				deadline = timer.C
			}
		case <-deadline:
			b.updateRuntime(id, "outcome_unknown", "interrupt_stop_unconfirmed", "")
			return false
		case <-c.Done():
			// Drain already delivered final notifications before treating disconnect as unknown.
			for {
				select {
				case n := <-c.Notifications():
					if b.consumeNotification(id, n) {
						return true
					}
				default:
					b.updateRuntime(id, "outcome_unknown", "codex_connection_lost", "")
					return false
				}
			}
		case n := <-c.Notifications():
			if b.consumeNotification(id, n) {
				return true
			}
		case <-tick.C:
			if b.runtime.cfg.Engine != nil && b.runtime.cfg.Engine.Device().Paused {
				b.mu.Lock()
				if j := b.runtime.jobs[id]; j.cancel != nil && !j.entry.Job.CancelRequested {
					j.cancel()
				}
				b.mu.Unlock()
			}
			b.refreshApprovals(id, c)
		}
	}
}
func (b *Bridge) consumeNotification(id string, n Notification) bool {
	var event struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
		Turn     struct {
			ID, Status string
			Items      []json.RawMessage
		} `json:"turn"`
		Item json.RawMessage `json:"item"`
	}
	if json.Unmarshal(n.Params, &event) != nil {
		return false
	}
	b.mu.Lock()
	j := b.runtime.jobs[id]
	thread, turn := j.entry.Job.ThreadID, j.entry.Job.TurnID
	b.mu.Unlock()
	if thread == "" || turn == "" || event.ThreadID != thread {
		return false
	}
	switch n.Method {
	case "turn/completed":
		if event.Turn.ID != turn || !terminalTurn(event.Turn.Status) {
			return false
		}
		// Completed items may appear only in the terminal event. Item IDs dedupe both paths.
		for _, item := range event.Turn.Items {
			b.collectItem(id, item)
		}
		b.finishTurn(id, event.Turn.Status)
		return true
	case "item/completed":
		if event.TurnID == turn {
			b.collectItem(id, event.Item)
		}
	case "item/started":
		if event.TurnID == turn && len(event.Item) <= 16384 {
			var item struct{ ID, Type string }
			if json.Unmarshal(event.Item, &item) == nil && item.Type == "fileChange" && item.ID != "" {
				b.mu.Lock()
				j := b.runtime.jobs[id]
				if j.reviewItems == nil {
					j.reviewItems = map[string]json.RawMessage{}
				}
				if len(j.reviewItems) < 64 {
					j.reviewItems[item.ID] = event.Item
				}
				b.mu.Unlock()
			}
		}
	}
	return false
}
func boundedText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 && (s[len(s)-1]&0xc0) == 0x80 {
		s = s[:len(s)-1]
	}
	return strings.ToValidUTF8(s, "")
}
func (b *Bridge) collectItem(id string, raw json.RawMessage) {
	var item struct {
		ID, Type, Status, Text string
		Source, Server, Tool   string
		AggregatedOutput       string `json:"aggregatedOutput"`
		ExitCode               *int   `json:"exitCode"`
		Result                 *struct {
			Content []struct {
				Type, Data, Text string
				MIME             string `json:"mimeType"`
			}
		} `json:"result"`
	}
	if json.Unmarshal(raw, &item) != nil || item.ID == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	j := b.runtime.jobs[id]
	r := j.entry.Job.Runtime
	for _, e := range r.Evidence {
		if e.ItemID == item.ID {
			return
		}
	}
	if len(r.Evidence) >= 128 {
		return
	}
	switch item.Type {
	case "agentMessage":
		r.Text = boundedText(r.Text+item.Text, maxResultText)
	case "commandExecution", "mcpToolCall", "fileChange":
	default:
		return
	}
	source, output := item.Source, item.AggregatedOutput
	if item.Type == "mcpToolCall" {
		source = item.Server + "/" + item.Tool
		if item.Result != nil {
			for _, content := range item.Result.Content {
				if content.Type == "text" {
					output = boundedText(output+content.Text, 8192)
				}
			}
		}
	}
	remaining := 65536
	for _, e := range r.Evidence {
		remaining -= len(e.Output)
	}
	if remaining > 8192 {
		remaining = 8192
	}
	if remaining < 0 {
		remaining = 0
	}
	r.Evidence = append(r.Evidence, model.CodexEvidence{Kind: item.Type, ItemID: boundedText(item.ID, 256), Status: boundedText(item.Status, 64), ExitCode: item.ExitCode, Source: boundedText(source, 256), Output: boundedText(output, remaining)})
	if item.Type == "mcpToolCall" && item.Result != nil {
		for _, content := range item.Result.Content {
			if content.Type != "image" || len(r.Images) >= maxResultImages || len(content.Data) > base64.StdEncoding.EncodedLen(maxImageBytes) {
				continue
			}
			if content.MIME != "image/png" && content.MIME != "image/jpeg" && content.MIME != "image/webp" && content.MIME != "image/gif" {
				continue
			}
			bytes, e := base64.StdEncoding.DecodeString(content.Data)
			if e != nil || len(bytes) == 0 || len(bytes) > maxImageBytes {
				continue
			}
			// Only native in-band base64 is accepted. Never open paths or fetch result URLs.
			r.Images = append(r.Images, model.Image{Data: content.Data, MIMEType: content.MIME})
			r.ImageDeliveryStatus = "received_in_band"
		}
	}
	j.entry.HadResults = true
	r.ResultStatus = "available"
	r.Verification = "unverified"
	// No screenshot, output, argument or model text is serialized by this append.
	_ = b.saveRuntimeLocked(j)
}
