package codexbridge

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/connections"
	"github.com/dotcontrol/dotcontrol/internal/engine"
	"github.com/dotcontrol/dotcontrol/internal/model"
)

func TestProfileRequestIsGatedWithoutChangingHistoricalLedger(t *testing.T) {
	dir := t.TempDir()
	b, err := New(Config{DeviceID: "device-a", StateDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	old, err := b.Submit(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "codex-jobs.jsonl"))
	var r model.CodexSubmit
	json.Unmarshal([]byte(`{"device_id":"device-a","request_id":"profile-request","profile_id":"exec-a","prompt":"Do one task"}`), &r)
	j, err := b.Submit(context.Background(), r)
	if err != nil || j.Code != "runtime_acceptance_required" {
		t.Fatalf("profile gate=%+v %v", j, err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "codex-jobs.jsonl"))
	if string(before) != string(after) {
		t.Fatal("new runtime changed historical ledger")
	}
	got, err := b.Job("device-a", old.JobID)
	if err != nil || got != old {
		t.Fatal("historical job changed")
	}
}

// These tests drive the real bridge over a fake JSON-RPC pipe; no desktop or
// remote machine is contacted and no model output stands in for a stop receipt.
type runtimePeer struct {
	mu                                sync.Mutex
	conn                              net.Conn
	methods                           []string
	params                            map[string][]json.RawMessage
	approvalPolicy                    string
	starts                            int
	lost                              bool
	takeover                          bool
	native, cuPresent, denyPermission bool
	readMessageID                     string
	blockMethod                       string
	blockEntered, blockRelease        chan struct{}
}

func (p *runtimePeer) send(v any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn != nil {
		_ = json.NewEncoder(p.conn).Encode(v)
	}
}
func (p *runtimePeer) notification(method string, params any) {
	p.send(map[string]any{"method": method, "params": params})
}
func (p *runtimePeer) count(method string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, m := range p.methods {
		if m == method {
			n++
		}
	}
	return n
}
func runtimeFixture(t *testing.T, p *runtimePeer) (Config, model.CodexSubmit) {
	t.Helper()
	// macOS temporary directories have a system /var symlink. The experiment
	// store intentionally accepts only physical state paths.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	store := connections.NewStore(dir)
	if err := store.SetCodexExecutable(os.Args[0]); err != nil {
		t.Fatal(err)
	}
	_, err = store.UpsertExecution(connections.ExecutionProfile{ID: "exec-a", Name: "Test", ProjectDir: project, Presentation: "background", PermissionProfile: "read-only", Execution: "terminal"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{DeviceID: "device-a", StateDir: dir, Runtime: &RuntimeConfig{ConnectionsDir: dir, Acceptance: map[string]PlatformAcceptance{runtime.GOOS: {Terminal: true, RemoteReview: true}}}}
	cfg.Runtime.StartClient = func(ctx context.Context, exe, cwd string) (*Client, error) {
		a, z := net.Pipe()
		p.mu.Lock()
		p.conn = z
		p.mu.Unlock()
		c := newClient(a, a, nil)
		go func() {
			defer z.Close()
			s := bufio.NewScanner(z)
			for s.Scan() {
				var r rpcMessage
				if json.Unmarshal(s.Bytes(), &r) != nil {
					return
				}
				p.mu.Lock()
				p.methods = append(p.methods, r.Method)
				if p.params == nil {
					p.params = map[string][]json.RawMessage{}
				}
				p.params[r.Method] = append(p.params[r.Method], append(json.RawMessage(nil), r.Params...))
				p.mu.Unlock()
				if r.Method == p.blockMethod && p.blockEntered != nil {
					close(p.blockEntered)
					<-p.blockRelease
				}
				if len(r.ID) == 0 {
					continue
				}
				var result any = map[string]any{}
				switch r.Method {
				case "permissionProfile/list":
					result = map[string]any{"data": []any{map[string]any{"id": "read-only", "allowed": !p.denyPermission}}}
				case "skills/list":
					result = map[string]any{"data": []any{map[string]any{"skills": []any{map[string]any{"name": "computer-use:computer-use", "enabled": p.cuPresent}}}}}
				case "thread/start":
					var params map[string]any
					json.Unmarshal(r.Params, &params)
					policy := p.approvalPolicy
					if policy == "" {
						policy = "on-request"
					}
					if params["ephemeral"] != false || params["approvalPolicy"] != policy || params["approvalsReviewer"] != "user" || params["permissions"] != "read-only" {
						t.Errorf("wrong thread contract: %v", params)
					}
					if _, ok := params["model"]; ok {
						t.Error("model settings overridden")
					}
					config, _ := params["config"].(map[string]any)
					if !p.native && config["features.computer_use"] != false {
						t.Error("terminal did not disable official computer use")
					}
					result = map[string]any{"thread": map[string]any{"id": "owned-thread"}}
				case "thread/resume":
					if p.takeover {
						p.send(map[string]any{"id": r.ID, "error": map[string]any{"code": -32600, "message": "thread owned-thread already has an active writer"}})
						continue
					}
					result = map[string]any{"thread": map[string]any{"id": "owned-thread"}}
				case "turn/start":
					p.mu.Lock()
					p.starts++
					turn := fmt.Sprintf("turn-%d", p.starts)
					lost := p.lost
					p.mu.Unlock()
					if lost {
						return
					}
					result = map[string]any{"turn": map[string]any{"id": turn, "status": "inProgress"}}
				case "thread/read":
					items := []any{}
					if p.readMessageID != "" {
						items = append(items, map[string]any{"type": "userMessage", "id": p.readMessageID})
					}
					result = map[string]any{"thread": map[string]any{"id": "owned-thread", "turns": []any{map[string]any{"id": "turn-1", "status": "completed", "items": items}}}}
				}
				p.send(map[string]any{"id": r.ID, "result": result})
			}
		}()
		return c, nil
	}
	return cfg, model.CodexSubmit{DeviceID: "device-a", RequestID: "runtime-a", ProfileID: "exec-a", Prompt: "private task"}
}
func waitRuntime(t *testing.T, b *Bridge, id string, predicate func(model.CodexJob) bool) model.CodexJob {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		j, e := b.Job("device-a", id)
		if e != nil {
			t.Fatal(e)
		}
		if predicate(j) {
			return j
		}
		time.Sleep(5 * time.Millisecond)
	}
	j, _ := b.Job("device-a", id)
	t.Fatalf("timed out: %+v runtime=%+v", j, j.Runtime)
	return j
}
func TestRuntimeUsesOwnedPersistentThreadAndBoundedPrivateResults(t *testing.T) {
	p := &runtimePeer{}
	cfg, r := runtimeFixture(t, p)
	b, e := New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	j, e := b.Submit(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID == "turn-1" })
	p.notification("item/completed", map[string]any{"threadId": j.ThreadID, "turnId": j.TurnID, "item": map[string]any{"type": "agentMessage", "id": "message", "text": "model says done"}})
	p.notification("item/completed", map[string]any{"threadId": "foreign", "turnId": j.TurnID, "item": map[string]any{"type": "agentMessage", "id": "foreign", "text": "foreign text"}})
	png := "iVBORw0KGgo="
	p.notification("item/completed", map[string]any{"threadId": j.ThreadID, "turnId": j.TurnID, "item": map[string]any{"type": "mcpToolCall", "id": "image", "status": "completed", "result": map[string]any{"content": []any{map[string]any{"type": "image", "data": png, "mimeType": "image/png"}}}}})
	p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "completed"}})
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.State == "completed" })
	if j.Runtime.Verification != "unverified" || j.Runtime.Text != "model says done" || len(j.Runtime.Images) != 1 {
		t.Fatalf("result=%+v", j.Runtime)
	}
	retry, e := b.Submit(context.Background(), r)
	if e != nil || retry.JobID != j.JobID || p.count("turn/start") != 1 {
		t.Fatal("duplicate turn")
	}
	b.Close()
	data, _ := os.ReadFile(filepath.Join(cfg.StateDir, "codex-runtime-v1.jsonl"))
	for _, secret := range []string{r.Prompt, png, "model says done"} {
		if strings.Contains(string(data), secret) {
			t.Fatal("private result journaled")
		}
	}
	b, e = New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	j, _ = b.Job("device-a", j.JobID)
	if len(j.Runtime.Images) != 0 || j.Runtime.ResultStatus != "unavailable_after_restart" {
		t.Fatal("restart claimed retained image")
	}
}
func TestUnknownTurnStartNeverReplaysAfterRestart(t *testing.T) {
	p := &runtimePeer{lost: true}
	cfg, r := runtimeFixture(t, p)
	b, e := New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	j, e := b.Submit(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.State == "outcome_unknown" })
	b.Close()
	p.lost = false
	b, e = New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	waitRuntimeInactive(t, b, j.JobID)
	_, e = b.Submit(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	if p.count("turn/start") != 1 || p.count("thread/start") != 1 {
		t.Fatal("unknown operation replayed")
	}
	if _, e = b.Continue(context.Background(), model.CodexContinue{DeviceID: "device-a", JobID: j.JobID, RequestID: "next", Prompt: "continue"}); e == nil {
		t.Fatal("unknown turn continued")
	}
}
func TestCancelAcknowledgementDoesNotProveStopped(t *testing.T) {
	p := &runtimePeer{}
	cfg, r := runtimeFixture(t, p)
	b, e := New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	j, e := b.Submit(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID != "" })
	j, e = b.Cancel("device-a", j.JobID)
	if e != nil {
		t.Fatal(e)
	}
	time.Sleep(80 * time.Millisecond)
	j, _ = b.Job("device-a", j.JobID)
	if j.State == "cancelled" || !j.CancelRequested || p.count("turn/interrupt") != 1 {
		t.Fatalf("cancel ack=%+v", j)
	}
	p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "interrupted"}})
	waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.State == "cancelled" })
}
func TestContinueBindsRequestAndClassifiesLocalTakeover(t *testing.T) {
	p := &runtimePeer{}
	cfg, r := runtimeFixture(t, p)
	b, e := New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	j, _ := b.Submit(context.Background(), r)
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID != "" })
	p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "completed"}})
	waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.State == "completed" })
	// A terminal event is durable before the old App Server writer finishes closing.
	// A new request may continue only after that cleanup has completed.
	waitRuntimeInactive(t, b, j.JobID)
	p.takeover = true
	next := model.CodexContinue{DeviceID: "device-a", JobID: j.JobID, RequestID: "continue-one", Prompt: "next private prompt"}
	if _, e = b.Continue(context.Background(), next); e != nil {
		t.Fatal(e)
	}
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.State == "local_takeover" })
	if j.Code != "local_takeover" || p.count("turn/start") != 1 {
		t.Fatal("forced local takeover")
	}
	if _, e = b.Continue(context.Background(), next); e != nil {
		t.Fatal(e)
	}
	next.Prompt = "changed"
	if _, e = b.Continue(context.Background(), next); e == nil {
		t.Fatal("changed retry accepted")
	}
}

func TestHumanReviewIsBoundAndStaleResponsesCannotApprove(t *testing.T) {
	p := &runtimePeer{}
	cfg, r := runtimeFixture(t, p)
	b, e := New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	j, _ := b.Submit(context.Background(), r)
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID != "" })
	p.send(map[string]any{"id": "approval-rpc", "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": j.ThreadID, "turnId": j.TurnID, "itemId": "command-a", "startedAtMs": time.Now().UnixMilli(), "command": "echo bounded", "cwd": t.TempDir(), "availableDecisions": []string{"accept", "decline", "cancel"}}})
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return len(j.Runtime.Approvals) == 1 })
	approval := j.Runtime.Approvals[0].ApprovalID
	if _, e = b.Review(context.Background(), "device-a", j.JobID, approval, nil); e == nil || e.Error() != "human_review_required" {
		t.Fatalf("nil human=%v", e)
	}
	if _, e = b.Review(context.Background(), "device-a", j.JobID, approval, func(ctx context.Context, p model.ReviewPrompt) (model.ReviewAnswer, error) {
		return model.ReviewAnswer{}, errors.New("human_input_required")
	}); e == nil || e.Error() != "human_input_required" {
		t.Fatalf("callback retry=%v", e)
	}
	started := make(chan struct{})
	answer := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, e := b.Review(context.Background(), "device-a", j.JobID, approval, func(ctx context.Context, prompt model.ReviewPrompt) (model.ReviewAnswer, error) {
			if prompt.ThreadID != j.ThreadID || prompt.TurnID != j.TurnID || !strings.Contains(prompt.Message, "echo bounded") {
				t.Error("unbound review")
			}
			close(started)
			<-answer
			return model.ReviewAnswer{Action: "accept", Content: map[string]any{"decision": "accept"}}, nil
		})
		done <- e
	}()
	<-started
	p.notification("serverRequest/resolved", map[string]any{"requestId": "approval-rpc"})
	time.Sleep(50 * time.Millisecond)
	close(answer)
	if e = <-done; e == nil {
		t.Fatal("stale review accepted")
	}
	p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "completed"}})
	waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.State == "completed" })
}
func TestSecretAndUnboundElicitationsRemainAtTarget(t *testing.T) {
	p := &runtimePeer{}
	cfg, r := runtimeFixture(t, p)
	b, e := New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	j, _ := b.Submit(context.Background(), r)
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID != "" })
	p.send(map[string]any{"id": "secret-rpc", "method": "mcpServer/elicitation/request", "params": map[string]any{"threadId": j.ThreadID, "turnId": j.TurnID, "serverName": "app", "mode": "form", "message": "Enter password", "requestedSchema": map[string]any{"type": "object", "properties": map[string]any{"password": map[string]any{"type": "string"}}}}})
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return len(j.Runtime.Approvals) == 1 })
	if j.Runtime.Approvals[0].State != "awaiting_target" {
		t.Fatal("secret forwarded")
	}
	called := false
	_, e = b.Review(context.Background(), "device-a", j.JobID, j.Runtime.Approvals[0].ApprovalID, func(context.Context, model.ReviewPrompt) (model.ReviewAnswer, error) {
		called = true
		return model.ReviewAnswer{}, nil
	})
	if e == nil || called {
		t.Fatal("secret review opened")
	}
	p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "interrupted"}})
	waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.State == "cancelled" })
}

func TestNativeMissingInventoryBlocksBeforeThreadOrLease(t *testing.T) {
	p := &runtimePeer{native: true}
	cfg, r := runtimeFixture(t, p)
	configureNative(t, &cfg)
	b, e := New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	j, _ := b.Submit(context.Background(), r)
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return runtimeTerminal(j.State) })
	if j.Code != "official_computer_use_unavailable" || p.count("thread/start") != 0 {
		t.Fatalf("missing native inventory=%+v", j)
	}
}

type runtimeMonitor struct{}

func (runtimeMonitor) Snapshot() model.Activity {
	return model.Activity{Available: true, IdleMillis: 120000}
}

type runtimeDriver struct{}

func (runtimeDriver) Apps(context.Context) (json.RawMessage, error) {
	return json.RawMessage(`[]`), nil
}
func (runtimeDriver) Observe(context.Context, model.Target) (model.View, error) {
	return model.View{}, nil
}
func (runtimeDriver) SupportsBackground(model.Action, model.View) bool { return false }
func (runtimeDriver) Check(context.Context, model.View) error          { return nil }
func (runtimeDriver) Execute(context.Context, model.Action, model.View, string) model.Outcome {
	return model.Outcome{}
}
func (runtimeDriver) Release(context.Context) error { return nil }
func configureNative(t *testing.T, cfg *Config) {
	t.Helper()
	_, err := connections.NewStore(cfg.Runtime.ConnectionsDir).UpsertExecution(connections.ExecutionProfile{ID: "exec-a", Name: "Native", ProjectDir: t.TempDir(), Presentation: "background", PermissionProfile: "read-only", Execution: "computer_use", AllowedApps: []string{"Calculator"}})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Runtime.Acceptance = map[string]PlatformAcceptance{runtime.GOOS: {NativeComputerUse: true, RemoteReview: true}}
	e, err := engine.New(engine.Config{DeviceID: "device-a", StateDir: t.TempDir()}, runtimeDriver{}, runtimeMonitor{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close() })
	cfg.Runtime.Engine = e
}
func TestNativeUnknownStopKeepsLeaseUntilOwnedReadConfirms(t *testing.T) {
	p := &runtimePeer{native: true, cuPresent: true}
	cfg, r := runtimeFixture(t, p)
	configureNative(t, &cfg)
	b, e := New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	j, _ := b.Submit(context.Background(), r)
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID != "" })
	if _, e = cfg.Runtime.Engine.Apps(context.Background()); e == nil {
		t.Fatal("desktop entered active native lease")
	}
	p.mu.Lock()
	conn := p.conn
	p.mu.Unlock()
	conn.Close()
	waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.State == "outcome_unknown" })
	b.Close()
	if _, e = cfg.Runtime.Engine.Apps(context.Background()); e == nil || e.Error() != "external_execution_unresolved" {
		t.Fatalf("unknown native stop released=%v", e)
	}
	b, e = New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.State == "completed" })
	waitRuntimeInactive(t, b, j.JobID)
	if _, e = cfg.Runtime.Engine.Apps(context.Background()); e != nil {
		t.Fatalf("confirmed owned stop failed to release=%v", e)
	}
	if p.count("turn/start") != 1 || p.count("thread/start") != 1 {
		t.Fatal("recovery replayed native task")
	}
}
func TestPermissionAndPresentationFailClosedWithoutSideEffects(t *testing.T) {
	for _, kind := range []string{"permission", "visible", "platform"} {
		t.Run(kind, func(t *testing.T) {
			p := &runtimePeer{}
			cfg, r := runtimeFixture(t, p)
			switch kind {
			case "permission":
				p.denyPermission = true
			case "visible":
				r.Presentation = "visible"
			case "platform":
				cfg.Runtime.Acceptance = map[string]PlatformAcceptance{"another-os": {Terminal: true}}
			}
			b, e := New(cfg)
			if e != nil {
				t.Fatal(e)
			}
			defer b.Close()
			j, _ := b.Submit(context.Background(), r)
			waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.State == "blocked" })
			if p.count("thread/start") != 0 || p.count("turn/start") != 0 {
				t.Fatal("blocked request had side effects")
			}
		})
	}
}

func TestHumanReviewOneTimeResponseAndUnsafeChoiceRejected(t *testing.T) {
	p := &runtimePeer{}
	cfg, r := runtimeFixture(t, p)
	b, e := New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	j, _ := b.Submit(context.Background(), r)
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID != "" })
	p.send(map[string]any{"id": "approved-once", "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": j.ThreadID, "turnId": j.TurnID, "itemId": "command-a", "startedAtMs": time.Now().UnixMilli(), "command": "echo bounded", "cwd": t.TempDir()}})
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return len(j.Runtime.Approvals) == 1 })
	token := j.Runtime.Approvals[0].ApprovalID
	_, e = b.Review(context.Background(), "device-a", j.JobID, token, func(context.Context, model.ReviewPrompt) (model.ReviewAnswer, error) {
		return model.ReviewAnswer{Action: "accept", Content: map[string]any{"decision": "acceptForSession"}}, nil
	})
	if e == nil {
		t.Fatal("persistent approval accepted")
	}
	_, e = b.Review(context.Background(), "device-a", j.JobID, token, func(context.Context, model.ReviewPrompt) (model.ReviewAnswer, error) {
		return model.ReviewAnswer{Action: "accept", Content: map[string]any{"decision": "accept"}}, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	called := false
	_, e = b.Review(context.Background(), "device-a", j.JobID, token, func(context.Context, model.ReviewPrompt) (model.ReviewAnswer, error) {
		called = true
		return model.ReviewAnswer{Action: "accept"}, nil
	})
	if e == nil || called {
		t.Fatal("one-time review replayed")
	}
	p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "completed"}})
	waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.State == "completed" })
}
func TestTargetPauseInterruptsTerminalTurnWithoutClaimingStop(t *testing.T) {
	p := &runtimePeer{}
	cfg, r := runtimeFixture(t, p)
	local, e := engine.New(engine.Config{DeviceID: "device-a", StateDir: t.TempDir()}, runtimeDriver{}, runtimeMonitor{})
	if e != nil {
		t.Fatal(e)
	}
	defer local.Close()
	cfg.Runtime.Engine = local
	b, e := New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	j, _ := b.Submit(context.Background(), r)
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID != "" })
	if e = local.Pause(true); e != nil {
		t.Fatal(e)
	}
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.CancelRequested })
	if j.State == "cancelled" {
		t.Fatal("pause acknowledgement claimed stop")
	}
	p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "interrupted"}})
	waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.State == "cancelled" })
}
func TestUnknownTurnNeverInfersOwnershipFromMessageID(t *testing.T) {
	p := &runtimePeer{lost: true}
	cfg, r := runtimeFixture(t, p)
	b, e := New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	j, _ := b.Submit(context.Background(), r)
	waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.State == "outcome_unknown" })
	b.Close()
	p.readMessageID = r.RequestID
	p.lost = false
	b, e = New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.State == "outcome_unknown" })
	if j.TurnID != "" || p.count("turn/start") != 1 {
		t.Fatal("inferred or replayed unknown turn")
	}
}

func TestToolEvidenceIncludesActualOutputWithoutVerifyingTask(t *testing.T) {
	p := &runtimePeer{}
	cfg, r := runtimeFixture(t, p)
	b, e := New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	j, _ := b.Submit(context.Background(), r)
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID != "" })
	p.notification("item/completed", map[string]any{"threadId": j.ThreadID, "turnId": j.TurnID, "item": map[string]any{"type": "commandExecution", "id": "actual-command", "source": "agent", "status": "completed", "exitCode": 0, "aggregatedOutput": "actual-private-output"}})
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return len(j.Runtime.Evidence) == 1 })
	data, _ := json.Marshal(j.Runtime.Evidence)
	if !strings.Contains(string(data), "actual-private-output") || j.Runtime.Verification != "unverified" {
		t.Fatalf("evidence=%s", data)
	}
	ledger, _ := os.ReadFile(filepath.Join(cfg.StateDir, "codex-runtime-v1.jsonl"))
	if strings.Contains(string(ledger), "actual-private-output") {
		t.Fatal("tool output journaled")
	}
	p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "completed"}})
	waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.State == "completed" })
}

func TestCompletedNativeJournalStillRecoversPendingDesktopLease(t *testing.T) {
	p := &runtimePeer{native: true, cuPresent: true}
	cfg, r := runtimeFixture(t, p)
	configureNative(t, &cfg)
	b, e := New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	j, _ := b.Submit(context.Background(), r)
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID != "" })
	p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "completed"}})
	waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.State == "completed" })
	b.Close()
	// Simulate crash between durable terminal event and durable desktop release.
	b, e = New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	lease, e := cfg.Runtime.Engine.AcquireDelegation(context.Background(), j.JobID)
	if e != nil {
		t.Fatal(e)
	}
	if e = lease.Finish(false); e != nil {
		t.Fatal(e)
	}
	b.mu.Lock()
	b.runtime.jobs[j.JobID].entry.DesktopPending = true
	e = b.saveRuntimeLocked(b.runtime.jobs[j.JobID])
	b.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	b.Close()
	before := p.count("thread/read")
	b, e = New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, e = cfg.Runtime.Engine.Apps(context.Background()); e == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if e != nil || p.count("thread/read") != before+1 {
		t.Fatalf("terminal journal stranded native guard: %v reads=%d", e, p.count("thread/read")-before)
	}
}
func TestOnlyExplicitlySupportedOfficialVersionCanRun(t *testing.T) {
	for _, version := range []string{"0.144.0", "unknown", "0.162.0-alpha.17.3"} {
		if supportedRuntimeVersion(version, nil) {
			t.Fatalf("unaccepted version %s", version)
		}
	}
	if !supportedRuntimeVersion("0.162.0-alpha.17.2", nil) {
		t.Fatal("verified schema rejected")
	}
	if supportedRuntimeVersion("0.162.0-alpha.17.2", []string{"0.144.0"}) {
		t.Fatal("explicit support list ignored")
	}
}
