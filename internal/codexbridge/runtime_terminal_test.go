package codexbridge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/connections"
	"github.com/dotcontrol/dotcontrol/internal/engine"
	"github.com/dotcontrol/dotcontrol/internal/model"
)

func terminalMode(t *testing.T, cfg *Config, enabled bool, requests ...*model.CodexSubmit) {
	t.Helper()
	cfg.Runtime.TerminalOnly, cfg.Runtime.TerminalEnabled = true, enabled
	terminalSelection(t, cfg, "exec-a", "")
}

func terminalPolicy(t *testing.T, cfg Config, policy string) {
	t.Helper()
	store := connections.NewStore(cfg.Runtime.ConnectionsDir)
	stored, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	p := stored.ExecutionProfiles[0]
	p.ApprovalPolicy = policy
	if _, err = store.UpsertExecution(p); err != nil {
		t.Fatal(err)
	}
	refreshTerminalBinding(t, cfg)
}

func peerParams(t *testing.T, p *runtimePeer, method string, index int) map[string]any {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.params[method]) <= index {
		t.Fatalf("missing %s params", method)
	}
	var out map[string]any
	if err := json.Unmarshal(p.params[method][index], &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestTerminalStartAndResumeUseSelectedPolicyAndSamePermissionScope(t *testing.T) {
	for _, policy := range []string{"never"} {
		t.Run(policy, func(t *testing.T) {
			p := &runtimePeer{approvalPolicy: policy}
			cfg, r := runtimeFixture(t, p)
			if policy == "never" {
				terminalMode(t, &cfg, true, &r)
			}
			terminalPolicy(t, cfg, policy)
			b, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			j, err := b.Submit(context.Background(), r)
			if err != nil {
				t.Fatal(err)
			}
			j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID != "" })
			for index, method := range []string{"thread/start", "thread/resume"} {
				params := peerParams(t, p, method, 0)
				inventory := peerParams(t, p, "permissionProfile/list", index)
				wantPolicy := policy
				if wantPolicy == "" {
					wantPolicy = "on-request"
				}
				if params["approvalPolicy"] != wantPolicy {
					t.Errorf("policy = %v, want %s", params["approvalPolicy"], wantPolicy)
				}
				if params["cwd"] != inventory["cwd"] || !strings.HasSuffix(params["cwd"].(string), filepath.Join(".dotcontrol", "tasks", j.JobID)) {
					t.Errorf("inventory does not describe the owned cwd: %v %v", inventory, params)
				}
				if roots, ok := params["runtimeWorkspaceRoots"]; ok && !reflect.DeepEqual(roots, []any{params["cwd"]}) {
					t.Errorf("runtime widened inventory workspace scope: %v", roots)
				}
				p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "completed"}})
				waitRuntimeInactive(t, b, j.JobID)
				if index == 0 {
					if _, err = b.Continue(context.Background(), model.CodexContinue{DeviceID: r.DeviceID, JobID: j.JobID, RequestID: "continue-terminal", Prompt: "Continue"}); err != nil {
						t.Fatal(err)
					}
					j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID == "turn-2" })
				}
			}
			if p.count("skills/list") != 0 {
				t.Fatal("terminal queried computer-use skills")
			}
		})
	}
}

func TestTerminalModeAdmissionIsImmutableAndKeepsAcceptanceGate(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		enabled, native, accepted bool
		code                      string
	}{
		{"disabled", false, false, true, "terminal_disabled"},
		{"computer_use", true, true, true, "terminal_only"},
		{"unaccepted", true, false, false, "runtime_acceptance_required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &runtimePeer{native: tc.native, cuPresent: true}
			cfg, r := runtimeFixture(t, p)
			if tc.native {
				configureNative(t, &cfg)
			}
			terminalMode(t, &cfg, tc.enabled)
			if !tc.accepted {
				cfg.Runtime.Acceptance = nil
			}
			b, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			terminalMode(t, &cfg, !tc.enabled)
			j, err := b.Submit(context.Background(), r)
			if err != nil {
				t.Fatal(err)
			}
			if j.State != "blocked" || j.Code != tc.code {
				t.Fatalf("admission = %+v, want %s", j, tc.code)
			}
			if p.count("initialize") != 0 || p.count("skills/list") != 0 || p.count("thread/start") != 0 {
				t.Fatal("blocked mode contacted Codex")
			}
		})
	}
}

type terminalMonitor struct{ activity model.Activity }

func (m terminalMonitor) Snapshot() model.Activity { return m.activity }

type terminalDriver struct{ runtimeDriver }

func (terminalDriver) Apps(context.Context) (json.RawMessage, error) { panic("terminal called Apps") }
func (terminalDriver) Observe(context.Context, model.Target) (model.View, error) {
	panic("terminal called Observe")
}
func (terminalDriver) Check(context.Context, model.View) error { panic("terminal called Check") }
func (terminalDriver) Execute(context.Context, model.Action, model.View, string) model.Outcome {
	panic("terminal called Execute")
}

func TestTerminalModeRunsWithoutDesktopAvailabilityAndHonorsGlobalPause(t *testing.T) {
	for _, tc := range []struct {
		name     string
		activity model.Activity
	}{{"unavailable", model.Activity{}}, {"locked", model.Activity{Available: true, Locked: true}}, {"active", model.Activity{Available: true, IdleMillis: 0}}} {
		t.Run(tc.name, func(t *testing.T) {
			p := &runtimePeer{approvalPolicy: "never"}
			cfg, r := runtimeFixture(t, p)
			terminalMode(t, &cfg, true, &r)
			terminalPolicy(t, cfg, "never")
			e, err := engine.New(engine.Config{DeviceID: r.DeviceID, StateDir: t.TempDir()}, terminalDriver{}, terminalMonitor{tc.activity})
			if err != nil {
				t.Fatal(err)
			}
			defer e.Close()
			cfg.Runtime.Engine = e
			b, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			j, err := b.Submit(context.Background(), r)
			if err != nil {
				t.Fatal(err)
			}
			j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID != "" })
			p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "completed"}})
			waitRuntimeInactive(t, b, j.JobID)
			if _, err = b.Continue(context.Background(), model.CodexContinue{DeviceID: r.DeviceID, JobID: j.JobID, RequestID: "pause-next", Prompt: "Continue"}); err != nil {
				t.Fatal(err)
			}
			j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID == "turn-2" })
			if err = e.Pause(true); err != nil {
				t.Fatal(err)
			}
			waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.CancelRequested && p.count("turn/interrupt") == 1 })
			p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "interrupted"}})
			waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.State == "cancelled" })
			if p.count("skills/list") != 0 {
				t.Fatal("terminal required a desktop skill")
			}
		})
	}
}

func terminalCapability(t *testing.T, b *Bridge) model.Capability {
	t.Helper()
	raw, _ := json.Marshal(b.Capabilities())
	var caps map[string]model.Capability
	json.Unmarshal(raw, &caps)
	return caps["codex_terminal"]
}

func TestTerminalCapabilitySeparatesConfiguredReadyFromConnected(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		enabled, accepted, paused bool
		state                     string
	}{
		{"ready", true, true, false, "ready"}, {"disabled", false, true, false, "disabled"},
		{"unaccepted", true, false, false, "unaccepted"}, {"paused", true, true, true, "paused"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &runtimePeer{}
			cfg, _ := runtimeFixture(t, p)
			terminalMode(t, &cfg, tc.enabled)
			terminalPolicy(t, cfg, "never")
			if !tc.accepted {
				cfg.Runtime.Acceptance = nil
			}
			e, err := engine.New(engine.Config{DeviceID: "device-a", StateDir: t.TempDir()}, terminalDriver{}, terminalMonitor{})
			if err != nil {
				t.Fatal(err)
			}
			defer e.Close()
			e.Pause(tc.paused)
			cfg.Runtime.Engine = e
			b, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			cap := terminalCapability(t, b)
			if cap.State != tc.state || cap.Available != (tc.state == "ready") {
				t.Fatalf("terminal capability=%+v", cap)
			}
			if b.Capabilities().CodexConnection.Available || p.count("initialize") != 0 {
				t.Fatal("configuration read pretended to connect")
			}
			if b.Capabilities().OfficialComputerUse.Available {
				t.Fatal("terminal advertised computer use")
			}
		})
	}
}

func TestNeverPolicyApprovalHaltsWithoutHumanResponse(t *testing.T) {
	for _, method := range []string{"item/commandExecution/requestApproval", "item/permissions/requestApproval", "mcpServer/elicitation/request", "item/tool/requestUserInput"} {
		t.Run(method, func(t *testing.T) {
			p := &runtimePeer{approvalPolicy: "never"}
			cfg, r := runtimeFixture(t, p)
			terminalMode(t, &cfg, true, &r)
			terminalPolicy(t, cfg, "never")
			b, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			j, _ := b.Submit(context.Background(), r)
			j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID != "" })
			p.send(map[string]any{"id": "unexpected-approval", "method": method, "params": map[string]any{"threadId": j.ThreadID, "turnId": j.TurnID, "command": "echo review", "cwd": t.TempDir(), "permissions": map[string]any{}, "startedAtMs": time.Now().UnixMilli()}})
			j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.CancelRequested && p.count("turn/interrupt") > 0 })
			if j.Code != "needs_local_setup" || len(j.Runtime.Approvals) != 0 || j.State == "blocked" {
				t.Fatalf("pending stop = %+v", j)
			}
			if p.count("") != 0 {
				t.Fatal("synthesized approval response")
			}
			p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "interrupted"}})
			waitRuntimeInactive(t, b, j.JobID)
			j, _ = b.Job(r.DeviceID, j.JobID)
			if j.State != "blocked" || j.Code != "needs_local_setup" || j.Runtime.TurnState != "interrupted" {
				t.Fatalf("setup stop=%+v", j)
			}
			if _, err = b.Continue(context.Background(), model.CodexContinue{DeviceID: r.DeviceID, JobID: j.JobID, RequestID: "unsafe-continue", Prompt: "retry"}); err == nil {
				t.Fatal("setup failure automatically continued")
			}
		})
	}
}

func TestTerminalImagesNotRequiredAcrossContinueAndRestart(t *testing.T) {
	p := &runtimePeer{approvalPolicy: "never"}
	cfg, r := runtimeFixture(t, p)
	terminalMode(t, &cfg, true, &r)
	terminalPolicy(t, cfg, "never")
	b, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	j, _ := b.Submit(context.Background(), r)
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID != "" })
	if j.Runtime.ImageDeliveryStatus != "not_required" {
		t.Errorf("terminal image status=%s", j.Runtime.ImageDeliveryStatus)
	}
	p.notification("item/completed", map[string]any{"threadId": j.ThreadID, "turnId": j.TurnID, "item": map[string]any{"id": "unexpected-image", "type": "mcpToolCall", "result": map[string]any{"content": []any{map[string]any{"type": "image", "mimeType": "image/png", "data": "iVBORw0KGgo="}}}}})
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return len(j.Runtime.Evidence) > 0 })
	if len(j.Runtime.Images) != 0 || j.Runtime.ImageDeliveryStatus != "not_required" {
		t.Fatal("terminal-only returned image bytes")
	}
	p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "completed"}})
	waitRuntimeInactive(t, b, j.JobID)
	continued, err := b.Continue(context.Background(), model.CodexContinue{DeviceID: r.DeviceID, JobID: j.JobID, RequestID: "continue-image", Prompt: "continue"})
	if err != nil {
		t.Fatal(err)
	}
	if continued.Runtime.ImageDeliveryStatus != "not_required" {
		t.Error("continuation reset not_required")
	}
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID == "turn-2" })
	p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "completed"}})
	waitRuntimeInactive(t, b, j.JobID)
	b.Close()
	b, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	j, _ = b.Job(r.DeviceID, j.JobID)
	if j.Runtime.ImageDeliveryStatus != "not_required" || j.Runtime.ResultStatus != "unavailable_after_restart" {
		t.Fatalf("restart status=%+v", j.Runtime)
	}
}

func TestLegacyTerminalDoesNotRequireNewEnableFlag(t *testing.T) {
	p := &runtimePeer{}
	cfg, r := runtimeFixture(t, p)
	cfg.Runtime.Acceptance = map[string]PlatformAcceptance{runtime.GOOS: {Terminal: true}}
	b, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	j, _ := b.Submit(context.Background(), r)
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID != "" })
	p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "completed"}})
	waitRuntimeInactive(t, b, j.JobID)
}

func TestTerminalModeChangesRequireNoUnsettledRuntime(t *testing.T) {
	p := &runtimePeer{approvalPolicy: "never"}
	cfg, r := runtimeFixture(t, p)
	terminalPolicy(t, cfg, "never")
	b, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	switcher, ok := any(b).(interface {
		ConfigureTerminal(bool, bool, string, string, string) error
	})
	if !ok {
		t.Fatal("local terminal mode switching is unavailable")
	}
	if err = switcher.ConfigureTerminal(true, true, "exec-a", "", refreshTerminalBinding(t, cfg)); err != nil {
		t.Fatal(err)
	}
	j, _ := b.Submit(context.Background(), r)
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID != "" })
	if err = switcher.ConfigureTerminal(true, false, "", "", ""); err == nil || err.Error() != "terminal_runtime_busy" {
		t.Fatalf("active switch=%v", err)
	}
	p.mu.Lock()
	connection := p.conn
	p.mu.Unlock()
	connection.Close()
	waitRuntimeInactive(t, b, j.JobID)
	if err = switcher.ConfigureTerminal(false, false, "", "", ""); err == nil || err.Error() != "terminal_runtime_busy" {
		t.Fatalf("unknown switch=%v", err)
	}
	b.mu.Lock()
	jState := b.runtime.jobs[j.JobID]
	jState.entry.Job.State = "completed"
	jState.entry.DesktopPending = true
	b.mu.Unlock()
	if err = switcher.ConfigureTerminal(false, false, "", "", ""); err == nil || err.Error() != "terminal_runtime_busy" {
		t.Fatalf("pending desktop switch=%v", err)
	}
	b.mu.Lock()
	jState.entry.DesktopPending = false
	b.mu.Unlock()
	if err = switcher.ConfigureTerminal(true, false, "", "", ""); err != nil {
		t.Fatal(err)
	}
	if cap := terminalCapability(t, b); cap.State != "disabled" {
		t.Fatalf("switch not applied=%+v", cap)
	}
}

func TestTerminalModeRequiresExplicitNeverPolicy(t *testing.T) {
	for _, policy := range []string{"", "on-request"} {
		t.Run(policy, func(t *testing.T) {
			p := &runtimePeer{}
			cfg, r := runtimeFixture(t, p)
			terminalMode(t, &cfg, true, &r)
			terminalPolicy(t, cfg, policy)
			b, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			j, err := b.Submit(context.Background(), r)
			if err != nil || j.State != "blocked" || j.Code != "needs_local_setup" || p.count("initialize") != 0 {
				t.Fatalf("unattended profile gate=%+v %v", j, err)
			}
		})
	}
}

func TestProbeWorkspaceSharesOwnedScopeWithoutExpandingProject(t *testing.T) {
	project := t.TempDir()
	path, err := ProbeWorkspace(project)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(canonical, ".dotcontrol", "tasks", "preflight") {
		t.Fatalf("probe cwd=%s", path)
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		t.Fatalf("probe cwd missing %v", err)
	}
	if again, err := ProbeWorkspace(project); err != nil || again != path {
		t.Fatal("probe workspace is unstable")
	}
	blocked := t.TempDir()
	if err := os.WriteFile(filepath.Join(blocked, ".dotcontrol"), []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ProbeWorkspace(blocked); err == nil {
		t.Fatal("overwrote existing project file")
	}
}

func TestNeverPolicyTerminalEventCannotHidePendingPermissionRequest(t *testing.T) {
	b, j, _, _ := isolatedRuntimeReview(t)
	b.mu.Lock()
	j.entry.Job.Runtime.Approvals = nil
	j.entry.Job.State = "running"
	b.mu.Unlock()
	raw, _ := json.Marshal(map[string]any{"threadId": j.entry.Job.ThreadID, "turn": map[string]any{"id": j.entry.Job.TurnID, "status": "completed"}})
	if !b.consumeNotification(j.entry.Job.JobID, Notification{Method: "turn/completed", Params: raw}) {
		t.Fatal("terminal event rejected")
	}
	got, err := b.Job("device-a", j.entry.Job.JobID)
	if err != nil || got.Code != "needs_local_setup" || got.State != "blocked" || !got.CancelRequested {
		t.Fatalf("pending approval hidden by terminal event: %+v %v", got, err)
	}
}

func TestNeverPolicyUnknownStopRetainsSetupFailureAcrossRecovery(t *testing.T) {
	p := &runtimePeer{approvalPolicy: "never"}
	cfg, r := runtimeFixture(t, p)
	terminalMode(t, &cfg, true, &r)
	terminalPolicy(t, cfg, "never")
	b, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	j, _ := b.Submit(context.Background(), r)
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID != "" })
	p.send(map[string]any{"id": "unwanted-permission", "method": "item/permissions/requestApproval", "params": map[string]any{"threadId": j.ThreadID, "turnId": j.TurnID, "permissions": map[string]any{}}})
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.State == "outcome_unknown" })
	if j.Code != "needs_local_setup" || !j.CancelRequested || len(j.Runtime.Approvals) > 0 {
		t.Fatalf("unconfirmed approval stop=%+v", j)
	}
	waitRuntimeInactive(t, b, j.JobID)
	for range 2 {
		// Exercise the shared cancellation transition directly as well as the
		// public idempotent wrapper after no runner remains to restore the code.
		b.mu.Lock()
		again, cancelErr := b.cancelRuntimeLocked(j.JobID)
		b.mu.Unlock()
		if cancelErr != nil || again.State != "outcome_unknown" || again.Code != "needs_local_setup" || again.Message != runtimeMessage("needs_local_setup") || !again.CancelRequested {
			t.Fatalf("repeat cancel lost setup cause=%+v %v", again, cancelErr)
		}
		again, cancelErr = b.Cancel(r.DeviceID, j.JobID)
		if cancelErr != nil || again.State != "outcome_unknown" || again.Code != "needs_local_setup" {
			t.Fatalf("public repeat cancel changed unknown cause=%+v %v", again, cancelErr)
		}
	}
	b.Close()
	b, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	waitRuntimeInactive(t, b, j.JobID)
	j, err = b.Job(r.DeviceID, j.JobID)
	if err != nil || j.State != "blocked" || j.Code != "needs_local_setup" || j.Runtime.ResultStatus != "unavailable_after_restart" {
		t.Fatalf("setup recovery=%+v %v", j, err)
	}
	again, err := b.Submit(context.Background(), r)
	if err != nil || again.JobID != j.JobID || p.count("turn/start") != 1 {
		t.Fatal("setup recovery replayed owned work")
	}
}

func TestTerminalModeSwitchRequiresGlobalPauseAndGatesContinuation(t *testing.T) {
	p := &runtimePeer{approvalPolicy: "never"}
	cfg, r := runtimeFixture(t, p)
	terminalPolicy(t, cfg, "never")
	e, err := engine.New(engine.Config{DeviceID: r.DeviceID, StateDir: t.TempDir()}, terminalDriver{}, terminalMonitor{})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	cfg.Runtime.Engine = e
	b, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err = b.ConfigureTerminal(true, true, "exec-a", "", refreshTerminalBinding(t, cfg)); err == nil || err.Error() != "terminal_mode_pause_required" {
		t.Fatalf("unpaused mode change=%v", err)
	}
	if err = e.Pause(true); err != nil {
		t.Fatal(err)
	}
	if err = b.ConfigureTerminal(true, true, "exec-a", "", refreshTerminalBinding(t, cfg)); err != nil {
		t.Fatal(err)
	}
	if err = e.Pause(false); err != nil {
		t.Fatal(err)
	}
	j, err := b.Submit(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID != "" })
	p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "completed"}})
	waitRuntimeInactive(t, b, j.JobID)
	if err = e.Pause(true); err != nil {
		t.Fatal(err)
	}
	if err = b.ConfigureTerminal(true, false, "", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Continue(context.Background(), model.CodexContinue{DeviceID: r.DeviceID, JobID: j.JobID, RequestID: "disabled-next", Prompt: "next"}); err == nil || err.Error() != "terminal_disabled" {
		t.Fatalf("disabled continuation=%v", err)
	}
	if p.count("thread/resume") != 0 {
		t.Fatal("disabled continuation contacted Codex")
	}
}
