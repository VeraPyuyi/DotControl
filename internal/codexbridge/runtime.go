package codexbridge

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/connections"
	"github.com/dotcontrol/dotcontrol/internal/engine"
	"github.com/dotcontrol/dotcontrol/internal/model"
)

func (b *Bridge) runtimeProfile(profileID, serverID string) (connections.Config, connections.ExecutionProfile, *connections.ServerProfile, string) {
	cfg, e := connections.NewStore(b.runtime.cfg.ConnectionsDir).Load()
	if e != nil {
		return cfg, connections.ExecutionProfile{}, nil, "connections_unavailable"
	}
	var p connections.ExecutionProfile
	for _, v := range cfg.ExecutionProfiles {
		if v.ID == profileID {
			p = v
			break
		}
	}
	if p.ID == "" {
		return cfg, p, nil, "profile_not_found"
	}
	if info, e := os.Stat(p.ProjectDir); e != nil || !info.IsDir() {
		return cfg, p, nil, "project_unavailable"
	}
	if info, e := os.Stat(cfg.CodexExecutable); e != nil || !info.Mode().IsRegular() {
		return cfg, p, nil, "codex_executable_unavailable"
	}
	if serverID != "" {
		return cfg, p, nil, "feature_retired"
	}
	var server *connections.ServerProfile
	return cfg, p, server, ""
}
func (b *Bridge) profileHash(cfg connections.Config, p connections.ExecutionProfile, s *connections.ServerProfile) string {
	data, _ := json.Marshal([]any{cfg.CodexExecutable, p, s})
	return b.hash("runtime-profile", data)
}

func effectiveApprovalPolicy(p connections.ExecutionProfile) string {
	if p.ApprovalPolicy == "" {
		return "on-request"
	}
	return p.ApprovalPolicy
}

// Admission is shared by submissions and continuations. Local terminal mode
// never grants permissions or changes the independent acceptance decision.
func (b *Bridge) runtimeAdmission(cfg connections.Config, p connections.ExecutionProfile, s *connections.ServerProfile) string {
	if p.Execution == "terminal" && b.runtime.cfg.TerminalOptInRequired && (!b.runtime.cfg.TerminalOnly || !b.runtime.cfg.TerminalEnabled) {
		return "terminal_disabled"
	}
	if b.runtime.cfg.TerminalOnly {
		if p.Execution != "terminal" {
			return "terminal_only"
		}
		if !b.runtime.cfg.TerminalEnabled {
			return "terminal_disabled"
		}
	}
	a := b.acceptance()
	if (p.Execution == "terminal" && !a.Terminal) || (p.Execution == "computer_use" && !a.NativeComputerUse) {
		return "runtime_acceptance_required"
	}
	if p.ApprovalPolicy != "never" {
		return "needs_local_setup"
	}
	if b.runtime.cfg.TerminalOnly {
		selected := b.runtime.cfg
		if !validID.MatchString(selected.TerminalProfileID) || !terminalConfigHashPattern.MatchString(selected.TerminalConfigHash) {
			return "needs_local_setup"
		}
		serverID := ""
		if s != nil {
			serverID = s.ID
		}
		if p.ID != selected.TerminalProfileID || serverID != selected.TerminalServerID {
			return "terminal_selection_mismatch"
		}
		if TerminalConfigHash(cfg) != selected.TerminalConfigHash {
			return "terminal_configuration_changed"
		}
	}
	if p.Execution == "computer_use" && b.runtime.cfg.Engine == nil {
		return "desktop_lease_unavailable"
	}
	return ""
}
func (b *Bridge) submitRuntime(ctx context.Context, r model.CodexSubmit) (model.CodexJob, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.healthy(); err != nil {
		return model.CodexJob{}, err
	}
	if b.runtime.closing {
		return model.CodexJob{}, failure("closed")
	}
	if r.DeviceID != b.deviceID {
		return model.CodexJob{}, failure("wrong_device")
	}
	if !validID.MatchString(r.RequestID) {
		return model.CodexJob{}, failure("invalid_request_id")
	}
	raw, _ := json.Marshal(r)
	hash := b.hash("request", raw)
	if _, ok := b.requests[r.RequestID]; ok {
		return model.CodexJob{}, failure("request_id_conflict")
	}
	if id, ok := b.runtime.requests[r.RequestID]; ok {
		j := b.runtime.jobs[id]
		if j.entry.Requests[r.RequestID] != hash {
			return model.CodexJob{}, failure("request_id_conflict")
		}
		return cloneJob(j.entry.Job), nil
	}
	if r.RetiredFieldsPresent || r.ServerProfileID != "" || r.ExperimentID != "" {
		return model.CodexJob{}, failure("feature_retired")
	}
	if ctx.Err() != nil {
		return model.CodexJob{}, failure("cancelled")
	}
	state, code := "queued", "request_persisted"
	var cfg connections.Config
	var p connections.ExecutionProfile
	var server *connections.ServerProfile
	if len(r.Prompt) > 65536 || strings.TrimSpace(r.Prompt) == "" || !validID.MatchString(r.ProfileID) {
		state, code = "rejected", "invalid_request"
	} else if r.Presentation != "" && r.Presentation != "visible" && r.Presentation != "background" {
		state, code = "rejected", "invalid_presentation"
	} else if len(r.AllowedApps) > 0 || len(r.WorkingDirs) > 0 {
		state, code = "rejected", "profile_scope_override_forbidden"
	} else if b.runtime.cfg.ConnectionsDir == "" {
		state, code = "blocked", "runtime_acceptance_required"
	} else {
		var reason string
		cfg, p, server, reason = b.runtimeProfile(r.ProfileID, r.ServerProfileID)
		if reason != "" {
			state, code = "blocked", reason
		} else {
			if reason = b.runtimeAdmission(cfg, p, server); reason != "" {
				state, code = "blocked", reason
			}
		}
	}
	presentation := r.Presentation
	if presentation == "" {
		presentation = p.Presentation
	}
	if presentation == "" {
		presentation = "visible"
	}
	if state == "queued" && presentation == "visible" && b.runtime.cfg.OpenThread == nil {
		state, code = "blocked", "visible_presentation_unavailable"
	}
	id, e := randomJobID()
	if e != nil {
		return model.CodexJob{}, e
	}
	now := time.Now().UTC()
	info := &model.CodexRuntimeInfo{Execution: p.Execution, Presentation: presentation, PresentationState: "pending", Verification: "unverified", ResultStatus: "pending"}
	if p.Execution == "terminal" {
		info.ImageDeliveryStatus = "not_required"
	}
	if state == "blocked" || state == "rejected" {
		info.PresentationState = "not_started"
	}
	j := &runtimeJob{entry: runtimeEntry{Version: 1, Phase: "intent", Requests: map[string]string{r.RequestID: hash}, CurrentRequestID: r.RequestID, Job: model.CodexJob{DeviceID: b.deviceID, JobID: id, RequestID: r.RequestID, State: state, Code: code, Message: runtimeMessage(code), CreatedAt: now, UpdatedAt: now, RequestHash: hash, PromptHash: b.hash("prompt", []byte(r.Prompt)), ProfileID: r.ProfileID, ServerProfileID: r.ServerProfileID, ExperimentID: r.ExperimentID, Runtime: info}}}
	if p.ID != "" {
		j.entry.ProfileHash = b.profileHash(cfg, p, server)
	}
	if e = b.saveRuntimeLocked(j); e != nil {
		return model.CodexJob{}, e
	}
	b.runtime.jobs[id] = j
	if state == "queued" {
		b.launchLocked(id, r.Prompt, false)
	}
	return cloneJob(j.entry.Job), nil
}
func runtimeMessage(code string) string {
	switch code {
	case "request_persisted":
		return "The owned request is recorded and queued."
	case "turn_running":
		return "The owned Codex turn is running."
	case "thread_start_pending", "thread_resume_pending", "turn_start_pending":
		return "The owned Codex task is starting."
	case "model_turn_completed":
		return "The Codex turn completed; task success remains unverified. Inspect returned evidence."
	case "local_takeover":
		return "The target Codex app owns this thread. Continue locally or release it before a new request."
	case "interrupt_requested":
		return "Interruption requested; stopping has not been confirmed."
	case "turn_interrupted":
		return "The owned Codex turn reported interruption."
	case "waiting_for_idle":
		return "Waiting for a continuous idle interval before acquiring the desktop."
	case "needs_local_setup":
		return "This terminal profile requires local setup. No human approval is forwarded; inspect the turn state to confirm whether interruption finished."
	case "terminal_only":
		return "This device is configured for terminal execution only."
	case "terminal_disabled":
		return "Terminal execution has not been enabled locally."
	case "terminal_selection_mismatch":
		return "This execution profile was not selected for terminal execution locally."
	case "terminal_configuration_changed":
		return "The saved terminal configuration changed after local validation. Validate it locally before enabling execution again."
	}
	return "Execution is unavailable or its outcome is not confirmed; see the status code."
}

// Only an official terminal receipt for the bound terminal turn can relax the
// cancellation latch. An interrupt acknowledgement or a pre-dispatch cancel
// does not prove that the owned turn stopped. The caller also checks live cleanup.
func continuableInterrupted(e runtimeEntry) bool {
	j := e.Job
	return e.Version == 1 && e.Phase == "turn_terminal" && !e.DesktopPending &&
		j.State == "cancelled" && j.Code == "turn_interrupted" && j.ThreadID != "" && j.TurnID != "" &&
		j.Runtime != nil && j.Runtime.Execution == "terminal" && j.Runtime.TurnState == "interrupted"
}

func (b *Bridge) Continue(ctx context.Context, r model.CodexContinue) (model.CodexJob, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, e := b.job(r.DeviceID, r.JobID); e != nil {
		return model.CodexJob{}, e
	}
	j := b.runtime.jobs[r.JobID]
	if j == nil {
		return model.CodexJob{}, failure("legacy_job_not_continuable")
	}
	if !validID.MatchString(r.RequestID) || strings.TrimSpace(r.Prompt) == "" || len(r.Prompt) > 65536 {
		return model.CodexJob{}, failure("invalid_request")
	}
	raw, _ := json.Marshal(r)
	hash := b.hash("continue", raw)
	if owner, ok := b.runtime.requests[r.RequestID]; ok {
		if owner != r.JobID || j.entry.Requests[r.RequestID] != hash {
			return model.CodexJob{}, failure("request_id_conflict")
		}
		return cloneJob(j.entry.Job), nil
	}
	if _, ok := b.requests[r.RequestID]; ok {
		return model.CodexJob{}, failure("request_id_conflict")
	}
	if j.entry.Job.ServerProfileID != "" || j.entry.Job.ExperimentID != "" || j.entry.ExperimentBinding != "" {
		return model.CodexJob{}, failure("feature_retired")
	}
	interrupted := continuableInterrupted(j.entry)
	if j.active || b.runtime.closing || j.client != nil || j.lease != nil || j.entry.DesktopPending || j.entry.Job.Runtime == nil || j.entry.Job.ThreadID == "" ||
		(!interrupted && (j.entry.Job.CancelRequested || (j.entry.Job.State != "completed" && j.entry.Job.State != "failed" && j.entry.Job.State != "local_takeover"))) {
		return model.CodexJob{}, failure("job_not_continuable")
	}
	if ctx.Err() != nil {
		return model.CodexJob{}, failure("cancelled")
	}
	cfg, p, s, code := b.runtimeProfile(j.entry.Job.ProfileID, j.entry.Job.ServerProfileID)
	if code != "" {
		return model.CodexJob{}, failure(code)
	}
	if b.profileHash(cfg, p, s) != j.entry.ProfileHash {
		return model.CodexJob{}, failure("profile_changed")
	}
	if interrupted && p.Execution != "terminal" {
		return model.CodexJob{}, failure("job_not_continuable")
	}
	if code := b.runtimeAdmission(cfg, p, s); code != "" {
		return model.CodexJob{}, failure(code)
	}
	// Prepare the complete intent separately: a failed append must not clear the
	// cancellation latch or consume this request in the live entry.
	next := &runtimeJob{entry: j.entry}
	next.entry.Job = cloneJob(j.entry.Job)
	next.entry.Requests = make(map[string]string, len(j.entry.Requests)+1)
	for id, requestHash := range j.entry.Requests {
		next.entry.Requests[id] = requestHash
	}
	next.entry.Requests[r.RequestID] = hash
	next.entry.CurrentRequestID = r.RequestID
	// A new request has no associated turn until its own start response is durable.
	// Recovery must never treat the previous request's terminal turn as its result.
	next.entry.Job.TurnID = ""
	next.entry.Phase = "continue_intent"
	next.entry.Job.State = "queued"
	next.entry.Job.Code = "request_persisted"
	next.entry.Job.Message = runtimeMessage("request_persisted")
	next.entry.Job.CancelRequested = false
	next.entry.Job.UpdatedAt = time.Now().UTC()
	next.entry.Job.Runtime.Text = ""
	next.entry.Job.Runtime.Images = nil
	next.entry.Job.Runtime.ImageDeliveryStatus = ""
	if p.Execution == "terminal" {
		next.entry.Job.Runtime.ImageDeliveryStatus = "not_required"
	}
	next.entry.Job.Runtime.ReviewLocation = ""
	next.entry.Job.Runtime.Evidence = nil
	next.entry.Job.Runtime.Approvals = nil
	next.entry.Job.Runtime.ResultStatus = "pending"
	next.entry.Job.Runtime.Verification = "unverified"
	next.entry.Job.Runtime.TurnState = ""
	if e := b.saveRuntimeLocked(next); e != nil {
		return model.CodexJob{}, e
	}
	j.entry = next.entry
	b.launchLocked(r.JobID, r.Prompt, false)
	return cloneJob(j.entry.Job), nil
}
func (b *Bridge) cancelRuntimeLocked(id string) (model.CodexJob, error) {
	j := b.runtime.jobs[id]
	j.entry.Job.CancelRequested = true
	j.entry.Job.UpdatedAt = time.Now().UTC()
	if !runtimeTerminal(j.entry.Job.State) {
		j.entry.Job.Code = "interrupt_requested"
		if j.entry.NeedsLocalSetup {
			j.entry.Job.Code = "needs_local_setup"
		}
		j.entry.Job.Message = runtimeMessage(j.entry.Job.Code)
	}
	if e := b.saveRuntimeLocked(j); e != nil {
		return model.CodexJob{}, e
	}
	if j.cancel != nil {
		j.cancel()
	}
	return cloneJob(j.entry.Job), nil
}
func (b *Bridge) launchLocked(id, prompt string, recoverOnly bool) {
	j := b.runtime.jobs[id]
	ctx, cancel := context.WithCancel(context.Background())
	j.cancel = cancel
	j.runContext = ctx
	j.active = true
	b.runtime.wg.Add(1)
	go func() { defer b.runtime.wg.Done(); b.run(ctx, id, prompt, recoverOnly) }()
}
func runtimeCall(c *Client, method string, params, out any) error {
	return runtimeCallContext(context.Background(), c, method, params, out)
}
func runtimeCallContext(parent context.Context, c *Client, method string, params, out any) error {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	return c.Call(ctx, method, params, out)
}
func (b *Bridge) updateRuntime(id, state, code, phase string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	j := b.runtime.jobs[id]
	if state != "" {
		j.entry.Job.State = state
	}
	if code != "" {
		if j.entry.NeedsLocalSetup {
			code = "needs_local_setup"
		}
		j.entry.Job.Code = code
		j.entry.Job.Message = runtimeMessage(code)
	}
	if phase != "" {
		j.entry.Phase = phase
	}
	j.entry.Job.UpdatedAt = time.Now().UTC()
	return b.saveRuntimeLocked(j) == nil
}
func (b *Bridge) run(ctx context.Context, id, prompt string, recoverOnly bool) {
	originalRun := ctx
	var c *Client
	var lease *engine.DelegationLease
	stopped := true
	connected := false
	nativeReady := false
	stopWatcher := func() {}
	if !recoverOnly {
		stopWatcher = b.watchRuntimeGuards(ctx, id)
	}
	defer func() {
		stopWatcher()
		b.mu.Lock()
		j := b.runtime.jobs[id]
		if j.runContext == originalRun {
			b.latchNativeInterruptionLocked(j)
		}
		if connected {
			b.runtime.connections--
		}
		if nativeReady {
			b.runtime.nativeConnections--
		}
		b.mu.Unlock()
		var releaseErr error
		if lease != nil {
			releaseErr = lease.Finish(stopped)
		}
		if c != nil {
			_ = c.Close()
		}
		b.mu.Lock()
		j = b.runtime.jobs[id]
		if j.runContext != originalRun {
			b.mu.Unlock()
			return
		}
		if lease != nil && stopped && releaseErr == nil {
			j.entry.DesktopPending = false
			_ = b.saveRuntimeLocked(j)
		} else if releaseErr != nil {
			j.entry.Job.Code = "desktop_release_unconfirmed"
			j.entry.Job.Message = runtimeMessage(j.entry.Job.Code)
			j.entry.Job.UpdatedAt = time.Now().UTC()
			_ = b.saveRuntimeLocked(j)
		}
		j.lease = nil
		j.client = nil
		j.entry.Job.Runtime.Approvals = nil
		b.mu.Unlock()
		if !recoverOnly && stopped && releaseErr == nil {
			b.presentFinishedRuntime(originalRun, id)
		}
		b.mu.Lock()
		defer b.mu.Unlock()
		j = b.runtime.jobs[id]
		if j.runContext != originalRun {
			return
		}
		if j.cancel != nil {
			j.cancel()
		}
		j.cancel = nil
		j.runContext = nil
		// Continue may acquire this job only after both the desktop lease and the
		// previous App Server writer have finished cleaning up.
		j.active = false
	}()
	b.mu.Lock()
	j := b.runtime.jobs[id]
	snapshot := cloneJob(j.entry.Job)
	profileHash := j.entry.ProfileHash
	cfg, p, server, code := b.runtimeProfile(snapshot.ProfileID, snapshot.ServerProfileID)
	b.mu.Unlock()
	preflightState := "blocked"
	if recoverOnly {
		preflightState = "outcome_unknown"
	}
	if code != "" {
		b.updateRuntime(id, preflightState, code, "")
		return
	}
	if b.profileHash(cfg, p, server) != profileHash {
		b.updateRuntime(id, preflightState, "profile_changed", "")
		return
	}
	if !recoverOnly {
		if code := b.runtimeAdmission(cfg, p, server); code != "" {
			b.updateRuntime(id, "blocked", code, "")
			return
		}
	}
	if !recoverOnly && b.runtime.cfg.Engine != nil && b.runtime.cfg.Engine.Device().Paused {
		b.updateRuntime(id, "blocked", "device_paused", "")
		return
	}
	start := b.runtime.cfg.StartClient
	if start == nil {
		versionCtx, done := context.WithTimeout(ctx, 4*time.Second)
		found, e := connections.DiscoverCodex(versionCtx, cfg.CodexExecutable, connections.DiscoveryOptions{})
		done()
		if e != nil || !supportedRuntimeVersion(found.Version, b.runtime.cfg.SupportedVersions) {
			b.updateRuntime(id, preflightState, "codex_version_not_accepted", "")
			return
		}
		start = Start
	}
	var err error
	c, err = start(context.Background(), cfg.CodexExecutable, p.ProjectDir)
	if err != nil {
		b.updateRuntime(id, preflightState, "codex_start_failed", "")
		return
	}
	b.mu.Lock()
	b.runtime.jobs[id].client = c
	b.mu.Unlock()
	var init json.RawMessage
	if err = runtimeCallContext(ctx, c, "initialize", map[string]any{"clientInfo": map[string]string{"name": "dotcontrol_owned_delegation", "version": model.Version}, "capabilities": map[string]bool{"experimentalApi": true}}, &init); err != nil {
		if recoverOnly {
			b.updateRuntime(id, "outcome_unknown", "owned_thread_read_unavailable", "")
			return
		}
		if !recoverOnly && !b.beforeRuntimeDispatch(ctx, id) {
			return
		}
		b.operationError(id, err, false)
		return
	}
	notifyCtx, notifyDone := context.WithTimeout(ctx, 10*time.Second)
	err = c.Notify(notifyCtx, "initialized", map[string]any{})
	notifyDone()
	if err != nil {
		if recoverOnly {
			b.updateRuntime(id, "outcome_unknown", "owned_thread_read_unavailable", "")
			return
		}
		if !recoverOnly && !b.beforeRuntimeDispatch(ctx, id) {
			return
		}
		b.operationError(id, err, false)
		return
	}
	b.mu.Lock()
	b.runtime.connections++
	connected = true
	b.mu.Unlock()
	if recoverOnly {
		stopped = b.recoverOwned(id, c)
		return
	}
	if ctx.Err() != nil {
		b.updateRuntime(id, "cancelled", "cancelled_before_execution", "")
		stopped = true
		return
	}
	if p.Execution == "computer_use" {
		if !officialCUSkill(ctx, c, p.ProjectDir) {
			b.updateRuntime(id, "blocked", "official_computer_use_unavailable", "")
			return
		}
		b.mu.Lock()
		b.runtime.nativeConnections++
		nativeReady = true
		b.mu.Unlock()
	}
	cwd, e := ownedWorkspace(p.ProjectDir, id)
	if e != nil {
		b.updateRuntime(id, "blocked", "task_directory_unavailable", "")
		return
	}
	permissionCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	profiles, e := ReadPermissionProfiles(permissionCtx, c, cwd)
	cancel()
	if e != nil {
		if !b.beforeRuntimeDispatch(ctx, id) {
			return
		}
		b.updateRuntime(id, "blocked", "permission_inventory_unavailable", "")
		return
	}
	allowed := false
	for _, v := range profiles {
		if v.ID == p.PermissionProfile && v.Allowed {
			allowed = true
		}
	}
	if !allowed {
		b.updateRuntime(id, "blocked", "permission_profile_unavailable", "")
		return
	}
	if p.Execution == "computer_use" {
		b.mu.Lock()
		b.runtime.jobs[id].entry.DesktopPending = true
		err = b.saveRuntimeLocked(b.runtime.jobs[id])
		b.mu.Unlock()
		if err != nil {
			return
		}
		lease, e = b.runtime.cfg.Engine.WaitAcquireDelegation(ctx, id, func() {
			b.updateRuntime(id, "waiting_for_idle", "waiting_for_idle", "")
		})
		if e != nil {
			b.mu.Lock()
			b.runtime.jobs[id].entry.DesktopPending = false
			b.mu.Unlock()
			if !b.beforeRuntimeDispatch(ctx, id) {
				return
			}
			b.updateRuntime(id, "blocked", e.Error(), "")
			return
		}
		b.mu.Lock()
		b.runtime.jobs[id].lease = lease
		b.mu.Unlock()
		ctx = lease.Context
	}
	if ctx.Err() != nil {
		b.updateRuntime(id, "cancelled", "cancelled_before_execution", "")
		stopped = true
		return
	}
	// Use the same cwd-derived permission roots as permissionProfile/list. A
	// project-root override here would silently widen the inspected owned scope.
	params := map[string]any{"cwd": cwd, "permissions": p.PermissionProfile, "approvalPolicy": effectiveApprovalPolicy(p), "approvalsReviewer": "user"}
	if p.Execution == "terminal" {
		params["config"] = map[string]any{"features.computer_use": false}
	}
	var result struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if snapshot.ThreadID == "" {
		if !b.updateRuntime(id, "starting", "thread_start_pending", "thread_intent") {
			return
		}
		params["ephemeral"] = false
		params["developerInstructions"] = taskInstructions(id, p)
		if !b.beforeRuntimeDispatch(ctx, id) {
			return
		}
		err = runtimeCallContext(ctx, c, "thread/start", params, &result)
	} else {
		if !b.updateRuntime(id, "starting", "thread_resume_pending", "resume_intent") {
			return
		}
		params["threadId"] = snapshot.ThreadID
		if !b.beforeRuntimeDispatch(ctx, id) {
			return
		}
		err = runtimeCallContext(ctx, c, "thread/resume", params, &result)
	}
	if err != nil {
		b.operationError(id, err, true)
		return
	}
	if result.Thread.ID == "" || (snapshot.ThreadID != "" && result.Thread.ID != snapshot.ThreadID) {
		b.updateRuntime(id, "outcome_unknown", "thread_binding_unknown", "")
		return
	}
	b.mu.Lock()
	j = b.runtime.jobs[id]
	j.entry.Job.ThreadID = result.Thread.ID
	j.entry.Phase = "thread_bound"
	err = b.saveRuntimeLocked(j)
	b.mu.Unlock()
	if err != nil {
		return
	}
	if snapshot.Runtime.Presentation != "visible" {
		b.mu.Lock()
		b.runtime.jobs[id].entry.Job.Runtime.PresentationState = "background"
		b.mu.Unlock()
	}
	if ctx.Err() != nil {
		b.updateRuntime(id, "cancelled", "cancelled_before_execution", "")
		stopped = true
		return
	}
	b.mu.Lock()
	j = b.runtime.jobs[id]
	j.entry.Job.TurnID = ""
	j.entry.Phase = "turn_intent"
	j.entry.Job.State = "starting"
	j.entry.Job.Code = "turn_start_pending"
	err = b.saveRuntimeLocked(j)
	requestID := j.entry.CurrentRequestID
	b.mu.Unlock()
	if err != nil {
		return
	}
	var turn struct {
		Turn struct{ ID, Status string } `json:"turn"`
	}
	if !b.beforeRuntimeDispatch(ctx, id) {
		return
	}
	stopped = false
	err = runtimeCallContext(ctx, c, "turn/start", map[string]any{"threadId": result.Thread.ID, "clientUserMessageId": requestID, "input": []any{map[string]any{"type": "text", "text": prompt, "text_elements": []any{}}}}, &turn)
	if err != nil {
		b.operationError(id, err, true)
		return
	}
	if turn.Turn.ID == "" {
		b.updateRuntime(id, "outcome_unknown", "turn_binding_unknown", "")
		return
	}
	b.mu.Lock()
	j = b.runtime.jobs[id]
	j.entry.Job.TurnID = turn.Turn.ID
	j.entry.Job.State = "running"
	j.entry.Job.Code = "turn_running"
	j.entry.Job.Message = runtimeMessage("turn_running")
	j.entry.Job.Runtime.TurnState = turn.Turn.Status
	j.entry.Phase = "turn_bound"
	err = b.saveRuntimeLocked(j)
	b.mu.Unlock()
	if err != nil {
		return
	}
	stopped = b.consume(ctx, id, c)
}

// Presentation is a post-execution action. The old writer and lease must be
// gone, while active/runContext still reserve this job against Continue.
func (b *Bridge) presentFinishedRuntime(run context.Context, id string) {
	b.mu.Lock()
	j := b.runtime.jobs[id]
	visible := j.entry.Job.Runtime.Presentation == "visible" && (j.entry.Job.State == "completed" || j.entry.Job.State == "failed")
	if j.runContext != run || b.runtimeInterruptedLocked(j) || j.entry.DesktopPending || j.client != nil || j.lease != nil || !visible || !terminalTurn(j.entry.Job.Runtime.TurnState) {
		b.mu.Unlock()
		return
	}
	thread, requestID := j.entry.Job.ThreadID, j.entry.CurrentRequestID
	open := b.runtime.cfg.OpenThread
	b.mu.Unlock()
	if open == nil || run.Err() != nil {
		return
	}
	ctx, cancel := context.WithTimeout(run, 10*time.Second)
	err := open(ctx, thread)
	cancel()
	b.mu.Lock()
	defer b.mu.Unlock()
	j = b.runtime.jobs[id]
	if j.runContext != run || j.entry.CurrentRequestID != requestID || b.runtimeInterruptedLocked(j) {
		return
	}
	j.entry.Job.Runtime.PresentationState = "open_requested"
	if err != nil {
		j.entry.Job.Runtime.PresentationState = "open_failed"
	}

	_ = b.saveRuntimeLocked(j)
}
func (b *Bridge) operationError(id string, err error, sideEffect bool) {
	var rpc *RPCError
	if errors.As(err, &rpc) && rpc.Code == -32600 && rpc.LocalTakeover {
		b.updateRuntime(id, "local_takeover", "local_takeover", "")
		return
	}
	var transport *TransportError
	if sideEffect && errors.As(err, &transport) && transport.OutcomeUnknown {
		b.updateRuntime(id, "outcome_unknown", "operation_outcome_unknown", "")
		return
	}
	b.updateRuntime(id, "failed", "codex_operation_failed", "")
}

// ProbeWorkspace gives local setup checks the same owned-directory semantics
// as a delegated task. The fixed leaf cannot select unrelated project paths.
func ProbeWorkspace(project string) (string, error) { return ownedWorkspace(project, "preflight") }

func ownedWorkspace(project, id string) (string, error) {
	canonical, e := filepath.EvalSymlinks(project)
	if e != nil {
		return "", e
	}
	current := canonical
	for _, part := range []string{".dotcontrol", "tasks", id} {
		current = filepath.Join(current, part)
		info, e := os.Lstat(current)
		if os.IsNotExist(e) {
			if e = os.Mkdir(current, 0700); e != nil {
				return "", e
			}
		} else if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", failure("task_directory_unavailable")
		}
		resolved, e := filepath.EvalSymlinks(current)
		if e != nil || !strings.EqualFold(filepath.Clean(resolved), filepath.Clean(current)) {
			return "", failure("task_directory_unavailable")
		}
	}
	return current, nil
}
func taskInstructions(id string, p connections.ExecutionProfile) string {
	text := "This is DotControl-owned task " + id + ". Work only on the user-authorized task in the independent current task directory. Check existing files and process/experiment records before launching work. Do not inspect unrelated chats, projects, credentials or other users' jobs. Report actual command/tool evidence separately from your conclusion. A finished model turn does not establish task or experiment success. Never claim verified completion from a PID, heartbeat or prose alone. Stop and report needs_local_setup when permission or scope is unclear."
	if p.Execution == "terminal" {
		text += " Official Computer Use is disabled. Do not use desktop/browser automation or substitute OS input tools."
	} else {
		apps, _ := json.Marshal(p.AllowedApps)
		text += " Use only official Computer Use tooling and these user-selected apps: " + string(apps) + ". This instruction is a scope constraint, not a technical app sandbox."
	}
	return text
}
func (b *Bridge) recoverOwned(id string, c *Client) bool {
	b.mu.Lock()
	j := cloneJob(b.runtime.jobs[id].entry.Job)
	b.mu.Unlock()
	var read struct {
		Thread struct {
			ID    string `json:"id"`
			Turns []struct {
				ID, Status string
				Items      []json.RawMessage
			} `json:"turns"`
		} `json:"thread"`
	}
	if runtimeCall(c, "thread/read", map[string]any{"threadId": j.ThreadID, "includeTurns": true}, &read) != nil || read.Thread.ID != j.ThreadID {
		b.updateRuntime(id, "outcome_unknown", "owned_thread_read_unavailable", "")
		return false
	}
	if j.TurnID == "" {
		b.updateRuntime(id, "outcome_unknown", "turn_binding_unknown", "")
		return false
	}
	for _, turn := range read.Thread.Turns {
		if turn.ID == j.TurnID && terminalTurn(turn.Status) {
			b.finishTurn(id, turn.Status)
			if j.Runtime.Execution == "computer_use" && b.runtime.cfg.Engine != nil {
				e := b.runtime.cfg.Engine.ConfirmDelegationStopped(id)
				if e == nil || e.Error() == "delegation_not_pending" {
					b.mu.Lock()
					b.runtime.jobs[id].entry.DesktopPending = false
					_ = b.saveRuntimeLocked(b.runtime.jobs[id])
					b.mu.Unlock()
				} else {
					b.updateRuntime(id, "", "desktop_release_unconfirmed", "")
				}
			}
			return true
		}
	}
	b.updateRuntime(id, "outcome_unknown", "owned_turn_stop_unconfirmed", "")
	return false
}

const SupportedAppServerVersion = "0.162.0-alpha.17.2"

func supportedRuntimeVersion(version string, accepted []string) bool {
	if len(accepted) == 0 {
		return version == SupportedAppServerVersion
	}
	for _, v := range accepted {
		if version == v {
			return true
		}
	}
	return false
}

// Presence is a prerequisite, not native execution acceptance. The independent
// per-platform gate must also be enabled before this read-only inventory runs.
func officialCUSkill(ctx context.Context, c *Client, cwd string) bool {
	var list struct {
		Data []struct {
			Skills []struct {
				Name    string
				Enabled bool
			}
		}
	}
	if runtimeCallContext(ctx, c, "skills/list", map[string]any{"cwds": []string{cwd}}, &list) != nil {
		return false
	}
	for _, group := range list.Data {
		for _, skill := range group.Skills {
			if skill.Enabled && (skill.Name == "computer-use:computer-use" || skill.Name == "computer-use") {
				return true
			}
		}
	}
	return false
}

// runContext is immutable for a particular launch, so it also serves as the
// generation binding for callbacks that outlive a turn or its client.
func (b *Bridge) runtimeInterruptedLocked(j *runtimeJob) bool {
	if j == nil || !j.active || j.runContext == nil || j.runContext.Err() != nil || j.entry.Job.CancelRequested || b.runtime.closing {
		return true
	}
	if j.lease != nil && j.lease.Context.Err() != nil {
		return true
	}
	return b.runtime.cfg.Engine != nil && b.runtime.cfg.Engine.Device().Paused
}

// A terminal event can be selected before consume sees lease cancellation.
// Latch the input interruption before cleanup drops the lease reference.
func (b *Bridge) latchNativeInterruptionLocked(j *runtimeJob) {
	if j.lease != nil && j.lease.Context.Err() != nil {
		j.entry.Job.CancelRequested = true
		if j.cancel != nil {
			j.cancel()
		}
	}
}

// The startup watcher runs independently of protocol calls. A temporary pause
// is latched even if the human resumes before an outstanding RPC returns.
func (b *Bridge) watchRuntimeGuards(run context.Context, id string) func() {
	ctx, cancel := context.WithCancel(run)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				b.mu.Lock()
				job := b.runtime.jobs[id]
				var client *Client
				if job != nil && job.runContext == run && job.active {
					client = job.client
				}
				b.mu.Unlock()
				if client != nil {
					b.interruptPendingRequests(id, client)
				}
				if b.runtime.cfg.Engine == nil || !b.runtime.cfg.Engine.Device().Paused {
					continue
				}
				b.mu.Lock()
				j := b.runtime.jobs[id]
				if j != nil && j.runContext == run && j.active && !runtimeTerminal(j.entry.Job.State) {
					j.entry.Job.CancelRequested = true
					j.entry.Job.Code = "interrupt_requested"
					j.entry.Job.Message = runtimeMessage("interrupt_requested")
					j.entry.Job.UpdatedAt = time.Now().UTC()
					if j.cancel != nil {
						j.cancel()
					}
					_ = b.saveRuntimeLocked(j)
				}
				b.mu.Unlock()
				return
			}
		}
	}()
	return func() { cancel(); <-done }
}

// Call only before starting a thread/turn or presenting a new window, never
// after a turn might already be running. Cancellation here proves no dispatch.
func (b *Bridge) beforeRuntimeDispatch(ctx context.Context, id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	j := b.runtime.jobs[id]
	if ctx.Err() == nil && !b.runtimeInterruptedLocked(j) {
		return true
	}
	if j == nil {
		return false
	}
	if j.cancel != nil {
		j.cancel()
	}
	j.entry.Job.CancelRequested = true
	j.entry.Job.State = "cancelled"
	j.entry.Job.Code = "cancelled_before_execution"
	j.entry.Job.Message = "Cancelled before the next Codex operation was dispatched."
	if j.entry.NeedsLocalSetup {
		j.entry.Job.State = "blocked"
		j.entry.Job.Code = "needs_local_setup"
		j.entry.Job.Message = runtimeMessage("needs_local_setup")
	}
	j.entry.Job.UpdatedAt = time.Now().UTC()
	_ = b.saveRuntimeLocked(j)
	return false
}
