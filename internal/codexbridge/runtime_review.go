package codexbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/model"
)

const reviewTTL = 5 * time.Minute

// LocalJobs inventories only this bridge's owned runtime records for the
// protected local UI. Results contain no private text, image bytes, or outputs.
// It never discovers arbitrary official threads and is not an MCP tool.
func (b *Bridge) LocalJobs(deviceID string) ([]model.CodexJob, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.healthy(); err != nil {
		return nil, err
	}
	if deviceID != b.deviceID {
		return nil, failure("wrong_device")
	}
	jobs := make([]model.CodexJob, 0, len(b.runtime.jobs))
	for _, j := range b.runtime.jobs {
		view := cloneJob(j.entry.Job)
		view.Runtime.Text = ""
		view.Runtime.Images = nil
		view.Runtime.Evidence = nil
		jobs = append(jobs, view)
	}
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].UpdatedAt.Equal(jobs[j].UpdatedAt) {
			return jobs[i].JobID < jobs[j].JobID
		}
		return jobs[i].UpdatedAt.After(jobs[j].UpdatedAt)
	})
	return jobs, nil
}

func sensitiveReview(s string) bool {
	lower := strings.ToLower(s)
	for _, pattern := range []string{"password", "passwd", "secret", "api_key", "apikey", "api-key", "access_token", "refresh_token", "authorization", "bearer ", "private key", "credential", "challenge", "client_secret", "sk-"} {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return strings.ContainsAny(s, "\x00")
}
func (b *Bridge) refreshApprovals(id string, c *Client) {
	pending := c.PendingRequests()
	b.mu.Lock()
	defer b.mu.Unlock()
	j := b.runtime.jobs[id]
	if j == nil || j.client != c || j.entry.Job.TurnID == "" || runtimeTerminal(j.entry.Job.State) {
		return
	}
	found := map[string]bool{}
	summaries := []model.CodexApproval{}
	for _, request := range pending {
		var params struct {
			ThreadID string `json:"threadId"`
			TurnID   string `json:"turnId"`
		}
		if json.Unmarshal(request.Params, &params) != nil || params.ThreadID != j.entry.Job.ThreadID || params.TurnID != j.entry.Job.TurnID {
			continue
		}
		raw, _ := json.Marshal(request)
		token := b.hash("approval:"+id, raw)
		found[token] = true
		approval := j.approvals[token]
		if approval == nil {
			approval = b.makeApproval(j, token, request)
			j.approvals[token] = approval
		}
		if approval.consumed {
			continue
		}
		state := "pending"
		if approval.prompt.Schema == nil {
			state = "awaiting_target"
		}
		if time.Now().After(approval.expires) {
			state = "expired"
		}
		summaries = append(summaries, model.CodexApproval{ApprovalID: token, Kind: approval.prompt.Kind, Operation: approvalKindOperation(approval.prompt.Kind), Scope: "owned_current_turn", State: state, ExpiresAt: approval.expires})
	}
	for token, a := range j.approvals {
		if !found[token] && !a.reviewing {
			delete(j.approvals, token)
		}
	}
	j.entry.Job.Runtime.Approvals = summaries
	if len(summaries) > 0 && !j.entry.Job.CancelRequested && !j.handoffRequested {
		j.entry.Job.State = "awaiting_approval"
		j.entry.Job.Code = "awaiting_human_review"
		j.entry.Job.Message = runtimeMessage("awaiting_human_review")
	} else if j.entry.Job.State == "awaiting_approval" {
		j.entry.Job.State = "running"
		j.entry.Job.Code = "turn_running"
	}
}
func approvalKindOperation(kind string) string {
	switch kind {
	case "command":
		return "Review a command requested by the owned turn"
	case "file":
		return "Review a file change requested by the owned turn"
	case "permissions":
		return "Review additional permissions for the owned turn"
	case "app":
		return "Review an app request from the owned turn"
	}
	return "Review on the target device"
}
func (b *Bridge) makeApproval(j *runtimeJob, token string, r ServerRequest) *runtimeApproval {
	now := time.Now().UTC()
	a := &runtimeApproval{request: r, expires: now.Add(reviewTTL)}
	p := &a.prompt
	p.DeviceID = b.deviceID
	p.JobID = j.entry.Job.JobID
	p.ThreadID = j.entry.Job.ThreadID
	p.TurnID = j.entry.Job.TurnID
	p.ApprovalID = token
	p.Kind = "unknown"
	p.Scope = "owned_current_turn"
	var v map[string]any
	if len(r.Params) > 32768 || json.Unmarshal(r.Params, &v) != nil {
		return a
	}
	if ts, ok := v["startedAtMs"].(float64); ok && ts > 0 {
		expires := time.UnixMilli(int64(ts)).Add(reviewTTL)
		if expires.Before(a.expires) {
			a.expires = expires
		}
	}
	p.ExpiresAt = a.expires
	if sensitiveReview(string(r.Params)) {
		return a
	}
	str := func(key string) string { s, _ := v[key].(string); return s }
	switch r.Method {
	case "item/commandExecution/requestApproval":
		p.Kind = "command"
		p.Operation = str("command")
		p.Scope = str("cwd")
		if p.Operation == "" || p.Scope == "" || len(p.Operation) > 8192 {
			return a
		}
		p.Choices = []string{"accept", "decline", "cancel"}
		if decisions, ok := v["availableDecisions"].([]any); ok {
			p.Choices = nil
			for _, d := range decisions {
				if s, ok := d.(string); ok && (s == "accept" || s == "decline" || s == "cancel") {
					p.Choices = append(p.Choices, s)
				}
			}
			if len(p.Choices) == 0 {
				return a
			}
		}
		detail, _ := json.Marshal(map[string]any{"command": v["command"], "cwd": v["cwd"], "reason": v["reason"], "additionalPermissions": v["additionalPermissions"], "networkApprovalContext": v["networkApprovalContext"]})
		p.Details = string(detail)
	case "item/fileChange/requestApproval":
		p.Kind = "file"
		p.Operation = "File change " + str("itemId")
		p.Scope = str("grantRoot")
		// The approval RPC lacks the patch. Forward only after a matching official
		// item/started supplies bounded actual changes, otherwise review at target.
		item := j.reviewItems[str("itemId")]
		if len(item) == 0 || len(item) > 16384 || sensitiveReview(string(item)) {
			return a
		}
		p.Details = string(item)
		p.Choices = []string{"accept", "decline", "cancel"}
		if p.Scope != "" {
			return a
		} // Session root grants are intentionally not forwarded.
	case "item/permissions/requestApproval":
		p.Kind = "permissions"
		p.Operation = "Additional permissions for this turn"
		p.Scope = str("cwd")
		permissions, ok := v["permissions"].(map[string]any)
		if !ok || len(permissions) == 0 {
			return a
		}
		for key := range permissions {
			if key != "fileSystem" && key != "network" {
				return a
			}
		}
		details, _ := json.Marshal(permissions)
		p.Details = string(details)
		p.Choices = []string{"accept", "decline"}
	case "mcpServer/elicitation/request":
		p.Kind = "app"
		p.Operation = str("message")
		p.Scope = str("serverName")
		if str("mode") != "form" || p.Operation == "" || p.Scope == "" {
			return a
		}
		schema, ok := v["requestedSchema"].(map[string]any)
		if !ok || !safeReviewSchema(schema) {
			return a
		}
		p.Schema = schema
		p.Message = p.Operation + "\nApp: " + p.Scope + "\nApproval applies only to this request."
		return a
	default:
		return a
	}
	p.Message = p.Operation + "\nScope: " + p.Scope + "\nDetails: " + p.Details + "\nApproval applies only to this request; no persistent rule is created."
	p.Schema = map[string]any{"type": "object", "properties": map[string]any{"decision": map[string]any{"type": "string", "enum": p.Choices}}, "required": []string{"decision"}}
	return a
}
func safeReviewSchema(s map[string]any) bool {
	if s["type"] != "object" {
		return false
	}
	raw, _ := json.Marshal(s)
	if len(raw) > 8192 || sensitiveReview(string(raw)) {
		return false
	}
	for k := range s {
		if k != "type" && k != "properties" && k != "required" && k != "additionalProperties" && k != "title" && k != "description" {
			return false
		}
	}
	props, _ := s["properties"].(map[string]any)
	if len(props) > 16 {
		return false
	}
	for _, v := range props {
		p, ok := v.(map[string]any)
		if !ok {
			return false
		}
		kind, _ := p["type"].(string)
		if kind != "string" && kind != "boolean" && kind != "integer" && kind != "number" {
			return false
		}
		for key := range p {
			switch key {
			case "type", "title", "description", "enum", "enumNames", "default", "minimum", "maximum", "minLength", "maxLength":
			default:
				return false
			}
		}
		if en, exists := p["enum"]; exists {
			values, ok := en.([]any)
			if !ok || len(values) > 32 {
				return false
			}
			for _, value := range values {
				if _, ok := value.(string); !ok {
					return false
				}
			}
		}
	}
	if required, ok := s["required"].([]any); ok {
		for _, v := range required {
			key, ok := v.(string)
			if !ok || props[key] == nil {
				return false
			}
		}
	}
	return true
}
func validReviewContent(schema any, content map[string]any) bool {
	s, ok := schema.(map[string]any)
	if !ok {
		return false
	}
	props, _ := s["properties"].(map[string]any)
	raw, e := json.Marshal(content)
	if e != nil || len(raw) > 16384 || sensitiveReview(string(raw)) {
		return false
	}
	var normalized map[string]any
	if json.Unmarshal(raw, &normalized) != nil {
		return false
	}
	for key, value := range normalized {
		v, exists := props[key]
		if !exists {
			return false
		}
		p, ok := v.(map[string]any)
		if !ok {
			return false
		}
		switch p["type"] {
		case "string":
			str, ok := value.(string)
			if !ok || len(str) > 4096 {
				return false
			}
			if min, ok := p["minLength"].(float64); ok && len(str) < int(min) {
				return false
			}
			if max, ok := p["maxLength"].(float64); ok && len(str) > int(max) {
				return false
			}
		case "boolean":
			if _, ok := value.(bool); !ok {
				return false
			}
		case "integer", "number":
			num, ok := value.(float64)
			if !ok || (p["type"] == "integer" && math.Trunc(num) != num) {
				return false
			}
			if min, ok := p["minimum"].(float64); ok && num < min {
				return false
			}
			if max, ok := p["maximum"].(float64); ok && num > max {
				return false
			}
		default:
			return false
		}
		if values, ok := p["enum"].([]any); ok {
			found := false
			for _, v := range values {
				if v == value {
					found = true
				}
			}
			if !found {
				return false
			}
		} else if values, ok := p["enum"].([]string); ok {
			found := false
			for _, v := range values {
				if v == value {
					found = true
				}
			}
			if !found {
				return false
			}
		}
	}
	switch required := s["required"].(type) {
	case []any:
		for _, key := range required {
			if _, ok := normalized[key.(string)]; !ok {
				return false
			}
		}
	case []string:
		for _, key := range required {
			if _, ok := normalized[key]; !ok {
				return false
			}
		}
	}
	return true
}
func requestPending(c *Client, r ServerRequest) bool {
	for _, p := range c.PendingRequests() {
		if bytes.Equal(p.ID, r.ID) && p.Method == r.Method && bytes.Equal(p.Params, r.Params) {
			return true
		}
	}
	return false
}
func (b *Bridge) Review(ctx context.Context, deviceID, jobID, approvalID string, human HumanReview) (model.CodexJob, error) {
	return b.review(ctx, deviceID, jobID, approvalID, human, false)
}

// ReviewLocal is reserved for the protected local human review UI. It must not
// be exposed as an MCP parameter that lets the model select a review channel.
// Local native review requires stopping and handing the owned thread over.
func (b *Bridge) ReviewLocal(ctx context.Context, deviceID, jobID, approvalID string, human HumanReview) (model.CodexJob, error) {
	return b.review(ctx, deviceID, jobID, approvalID, human, true)
}

func (b *Bridge) review(ctx context.Context, deviceID, jobID, approvalID string, human HumanReview, local bool) (model.CodexJob, error) {
	b.mu.Lock()
	if _, e := b.job(deviceID, jobID); e != nil {
		b.mu.Unlock()
		return model.CodexJob{}, e
	}
	j := b.runtime.jobs[jobID]
	if j == nil || j.client == nil {
		b.mu.Unlock()
		return model.CodexJob{}, failure("approval_not_found")
	}
	c := j.client
	b.mu.Unlock()
	b.refreshApprovals(jobID, c)
	b.mu.Lock()
	j = b.runtime.jobs[jobID]
	a := j.approvals[approvalID]
	if a == nil || a.consumed || a.reviewing || j.handoffRequested || b.runtimeInterruptedLocked(j) || runtimeTerminal(j.entry.Job.State) || time.Now().After(a.expires) || !requestPending(c, a.request) {
		b.mu.Unlock()
		return model.CodexJob{}, failure("approval_stale")
	}
	if a.prompt.Schema == nil || (local && j.entry.Job.Runtime.Execution != "terminal") {
		b.mu.Unlock()
		return model.CodexJob{}, failure("review_on_target_required")
	}
	if human == nil || (!local && !b.acceptance().RemoteReview) {
		b.mu.Unlock()
		return model.CodexJob{}, failure("human_review_required")
	}
	a.reviewing = true
	j.entry.Job.Runtime.ReviewLocation = "remote_human"
	if local {
		j.entry.Job.Runtime.ReviewLocation = "local_web"
	}
	originalRun := j.runContext
	originalRequestID := j.entry.CurrentRequestID
	prompt := a.prompt
	prompt.Choices = append([]string(nil), a.prompt.Choices...)
	schemaBytes, _ := json.Marshal(a.prompt.Schema)
	_ = json.Unmarshal(schemaBytes, &prompt.Schema)
	b.mu.Unlock()
	answer, err := human(ctx, prompt)
	b.mu.Lock()
	defer b.mu.Unlock()
	a.reviewing = false
	j = b.runtime.jobs[jobID]
	if err != nil {
		if err.Error() == "human_input_required" {
			return cloneJob(j.entry.Job), failure("human_input_required")
		}
		return cloneJob(j.entry.Job), failure("human_review_failed")
	}
	if ctx.Err() != nil || j.client != c || j.runContext != originalRun || j.entry.CurrentRequestID != originalRequestID || j.entry.Job.ThreadID != prompt.ThreadID || j.entry.Job.TurnID != prompt.TurnID || b.runtimeInterruptedLocked(j) || runtimeTerminal(j.entry.Job.State) || time.Now().After(a.expires) || a.consumed || !requestPending(c, a.request) {
		return model.CodexJob{}, failure("approval_stale")
	}
	if answer.Action != "accept" && answer.Action != "decline" && answer.Action != "cancel" {
		return model.CodexJob{}, failure("invalid_human_response")
	}
	var response any
	if a.prompt.Kind == "app" {
		if answer.Action == "accept" && !validReviewContent(a.prompt.Schema, answer.Content) {
			return model.CodexJob{}, failure("invalid_human_response")
		}
		if answer.Action == "accept" {
			response = map[string]any{"action": answer.Action, "content": answer.Content}
		} else {
			response = map[string]any{"action": answer.Action}
		}
	} else {
		decision := answer.Action
		if answer.Action == "accept" {
			if !validReviewContent(a.prompt.Schema, answer.Content) {
				return model.CodexJob{}, failure("invalid_human_response")
			}
			decision, _ = answer.Content["decision"].(string)
		}
		allowed := false
		for _, choice := range a.prompt.Choices {
			if choice == decision {
				allowed = true
			}
		}
		if !allowed {
			return model.CodexJob{}, failure("invalid_human_response")
		}
		if a.prompt.Kind == "permissions" {
			permissions := map[string]any{}
			if decision == "accept" {
				var v map[string]any
				_ = json.Unmarshal(a.request.Params, &v)
				permissions = v["permissions"].(map[string]any)
			}
			response = map[string]any{"permissions": permissions, "scope": "turn"}
		} else {
			response = map[string]any{"decision": decision}
		}
	}
	// Persist one-time response intent before writing. Neither a timeout nor a
	// missing acknowledgement authorizes replaying this human answer.
	a.consumed = true
	j.entry.Phase = "review_response_intent"
	j.entry.LastReviewID = approvalID
	if e := b.saveRuntimeLocked(j); e != nil {
		return model.CodexJob{}, e
	}
	// Network writes must not own Bridge.mu. A wedged stdio reader cannot block
	// Cancel, pause handling, or a terminal event from advancing this job.
	writeCtx, writeDone := context.WithTimeout(ctx, time.Second)
	stopRun := context.AfterFunc(originalRun, writeDone)
	stopLease := func() bool { return true }
	if j.lease != nil {
		stopLease = context.AfterFunc(j.lease.Context, writeDone)
	}
	if b.runtimeInterruptedLocked(j) {
		writeDone()
	}
	b.mu.Unlock()
	writeErr := c.Respond(writeCtx, a.request.ID, response)
	stopRun()
	stopLease()
	writeDone()
	b.mu.Lock()
	j = b.runtime.jobs[jobID]
	// The old response may finish after cancellation, a terminal event, a new
	// launch, or another approval. Return the current job without overwriting it.
	bound := j != nil && j.client == c && j.runContext == originalRun && j.entry.CurrentRequestID == originalRequestID && j.entry.Job.ThreadID == prompt.ThreadID && j.entry.Job.TurnID == prompt.TurnID && j.entry.LastReviewID == approvalID && j.entry.Phase == "review_response_intent" && !b.runtimeInterruptedLocked(j) && !runtimeTerminal(j.entry.Job.State) && j.entry.Job.State != "outcome_unknown"
	if !bound {
		if writeErr != nil {
			return cloneJob(j.entry.Job), failure("review_response_outcome_unknown")
		}
		return cloneJob(j.entry.Job), nil
	}
	if writeErr != nil {
		j.entry.Job.Code = "review_response_outcome_unknown"
		_ = b.saveRuntimeLocked(j)
		return cloneJob(j.entry.Job), failure("review_response_outcome_unknown")
	}
	j.entry.Phase = "turn_bound"
	j.entry.Job.Code = "human_response_sent"
	_ = b.saveRuntimeLocked(j)
	for i, v := range j.entry.Job.Runtime.Approvals {
		if v.ApprovalID == approvalID {
			j.entry.Job.Runtime.Approvals = append(j.entry.Job.Runtime.Approvals[:i], j.entry.Job.Runtime.Approvals[i+1:]...)
			break
		}
	}
	return cloneJob(j.entry.Job), nil
}

// TakeOverLocal is a trusted local UI action, never an approval RPC response.
// It binds the request to a current approval, requests interruption, and leaves
// the run to await official terminal evidence and clean up before presentation.
// No human-input exemption is granted to a native lease.
func (b *Bridge) TakeOverLocal(ctx context.Context, deviceID, jobID, approvalID string) (model.CodexJob, error) {
	b.mu.Lock()
	if _, err := b.job(deviceID, jobID); err != nil {
		b.mu.Unlock()
		return model.CodexJob{}, err
	}
	j := b.runtime.jobs[jobID]
	if j == nil || j.client == nil {
		b.mu.Unlock()
		return model.CodexJob{}, failure("approval_not_found")
	}
	c := j.client
	b.mu.Unlock()
	b.refreshApprovals(jobID, c)
	b.mu.Lock()
	defer b.mu.Unlock()
	j = b.runtime.jobs[jobID]
	a := j.approvals[approvalID]
	if ctx.Err() != nil || j.client != c || a == nil || a.consumed || a.reviewing || j.handoffRequested || j.handoff == nil || b.runtimeInterruptedLocked(j) || runtimeTerminal(j.entry.Job.State) || time.Now().After(a.expires) || !requestPending(c, a.request) {
		return model.CodexJob{}, failure("approval_stale")
	}
	if b.runtime.cfg.OpenThread == nil {
		return model.CodexJob{}, failure("visible_presentation_unavailable")
	}
	a.consumed = true
	j.handoffRequested = true
	j.entry.Job.State = "awaiting_target"
	j.entry.Job.Code = "interrupt_for_local_takeover"
	j.entry.Job.Message = runtimeMessage(j.entry.Job.Code)
	j.entry.Job.UpdatedAt = time.Now().UTC()
	j.entry.Phase = "local_takeover_intent"
	j.entry.LastReviewID = approvalID
	if err := b.saveRuntimeLocked(j); err != nil {
		return model.CodexJob{}, err
	}
	close(j.handoff)
	return cloneJob(j.entry.Job), nil
}
