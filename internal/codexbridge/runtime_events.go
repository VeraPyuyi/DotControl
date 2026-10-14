package codexbridge

import (
	"context"
	"crypto/sha256"
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
	if j.entry.NeedsLocalSetup {
		state, code = "blocked", "needs_local_setup"
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
	_ = b.saveRuntimeLocked(j)
}
func (b *Bridge) consume(ctx context.Context, id string, c *Client) bool {
	tick := time.NewTicker(40 * time.Millisecond)
	defer tick.Stop()
	cancelled := ctx.Done()
	var deadline <-chan time.Time
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		select {
		case <-cancelled:
			cancelled = nil
			b.mu.Lock()
			j := b.runtime.jobs[id]
			thread, turn := j.entry.Job.ThreadID, j.entry.Job.TurnID
			j.entry.Job.CancelRequested = true
			j.entry.Job.Code = "interrupt_requested"
			if j.entry.NeedsLocalSetup {
				j.entry.Job.Code = "needs_local_setup"
			}
			j.entry.Job.Message = runtimeMessage(j.entry.Job.Code)
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
			b.interruptPendingRequests(id, c)
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
		// A final notification can arrive before the periodic approval refresh.
		// Preserve unexpected permission requests even when a terminal receipt
		// wins that race; completion must not hide a broken unattended setup.
		b.mu.Lock()
		client := b.runtime.jobs[id].client
		b.mu.Unlock()
		if client != nil {
			b.interruptPendingRequests(id, client)
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

	}
	return false
}
func boundedText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// Removing an incomplete rune must not drop a complete Chinese character
	// whose final byte happens to be exactly at the byte limit.
	return strings.ToValidUTF8(s[:n], "")
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
	key := sha256.Sum256([]byte(item.ID))
	if _, seen := j.resultItems[key]; seen {
		return
	}
	for _, e := range r.Evidence {
		if e.ItemID == item.ID {
			return
		}
	}
	previousText, previousImages := len(r.Text), len(r.Images)
	previousEvidence := len(r.Evidence)
	textTruncated, evidenceTruncated := r.TextTruncated, r.EvidenceTruncated
	switch item.Type {
	case "agentMessage":
		r.TextTruncated = r.TextTruncated || len(item.Text) > maxResultText-len(r.Text)
		r.Text = boundedText(r.Text+item.Text, maxResultText)
	case "commandExecution", "mcpToolCall", "fileChange":
	default:
		return
	}
	source, output := item.Source, item.AggregatedOutput
	outputTruncated := false
	if item.Type == "mcpToolCall" {
		source = item.Server + "/" + item.Tool
		if item.Result != nil {
			for _, content := range item.Result.Content {
				if content.Type == "text" {
					outputTruncated = outputTruncated || len(content.Text) > 8192-len(output)
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
	if len(r.Evidence) < 128 {
		r.Evidence = append(r.Evidence, model.CodexEvidence{Kind: item.Type, ItemID: boundedText(item.ID, 256), Status: boundedText(item.Status, 64), ExitCode: item.ExitCode, Source: boundedText(source, 256), Output: boundedText(output, remaining), OutputTruncated: outputTruncated || len(output) > remaining})
	} else {
		r.EvidenceTruncated = true
	}
	if item.Type == "mcpToolCall" && item.Result != nil && !b.runtime.cfg.TerminalOnly {
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
	if len(r.Text) > previousText || len(r.Images) > previousImages {
		if j.resultItems == nil {
			j.resultItems = make(map[[32]byte]struct{})
		}
		j.resultItems[key] = struct{}{}
	}
	if previousText == len(r.Text) && previousImages == len(r.Images) && previousEvidence == len(r.Evidence) && textTruncated == r.TextTruncated && evidenceTruncated == r.EvidenceTruncated && j.entry.HadResults && r.ResultStatus == "available" && r.Verification == "unverified" {
		return // Saturated/discarded events must not grow the fsync-backed journal.
	}
	j.entry.HadResults = true
	r.ResultStatus = "available"
	r.Verification = "unverified"
	// No screenshot, output, argument or model text is serialized by this append.
	_ = b.saveRuntimeLocked(j)
}
