package codexbridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/dotcontrol/dotcontrol/internal/connections"
	"github.com/dotcontrol/dotcontrol/internal/model"
)

func terminalSelection(t *testing.T, cfg *Config, profile, server string) {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"TerminalProfileID": profile, "TerminalServerID": server})
	if err := json.Unmarshal(raw, cfg.Runtime); err != nil {
		t.Fatal(err)
	}
	refreshTerminalBinding(t, *cfg)
}

func refreshTerminalBinding(t *testing.T, cfg Config) string {
	t.Helper()
	c, err := connections.NewStore(cfg.Runtime.ConnectionsDir).Load()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(c)
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	raw, _ = json.Marshal(map[string]string{"TerminalConfigHash": hash})
	if err = json.Unmarshal(raw, cfg.Runtime); err != nil {
		t.Fatal(err)
	}
	return hash
}

func terminalBindingFixture(t *testing.T, p *runtimePeer) (Config, model.CodexSubmit) {
	t.Helper()
	cfg, r := runtimeFixture(t, p)
	terminalMode(t, &cfg, true)
	terminalPolicy(t, cfg, "never")
	store := connections.NewStore(cfg.Runtime.ConnectionsDir)
	c, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	other := c.ExecutionProfiles[0]
	other.ID = "exec-b"
	if _, err = store.UpsertExecution(other); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"server-a", "server-b"} {
		if _, err = store.UpsertServer(connections.ServerProfile{ID: id, Name: id, SSHAlias: "fixture.invalid", RemoteDir: "/test"}); err != nil {
			t.Fatal(err)
		}
	}
	terminalSelection(t, &cfg, "exec-a", "server-a")
	r.ServerProfileID = "server-a"
	return cfg, r
}

func TestTerminalEnabledAcceptsOnlySelectedExecutionServerPair(t *testing.T) {
	for _, tc := range []struct{ name, profile, server string }{{"other execution", "exec-b", "server-a"}, {"other server", "exec-a", "server-b"}, {"missing server", "exec-a", ""}} {
		t.Run(tc.name, func(t *testing.T) {
			p := &runtimePeer{approvalPolicy: "never"}
			cfg, r := terminalBindingFixture(t, p)
			b, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			r.ProfileID, r.ServerProfileID = tc.profile, tc.server
			j, err := b.Submit(context.Background(), r)
			if err != nil || j.State != "blocked" || j.Code != "terminal_selection_mismatch" {
				t.Fatalf("unprobed pair admitted=%+v %v", j, err)
			}
			if p.count("initialize") != 0 {
				t.Fatal("unprobed pair contacted Codex")
			}
		})
	}
}

func TestTerminalOptionalServerRetainsExactBindingAcrossRestart(t *testing.T) {
	p := &runtimePeer{approvalPolicy: "never"}
	cfg, r := terminalBindingFixture(t, p)
	terminalSelection(t, &cfg, "exec-a", "")
	for _, requestID := range []string{"local-terminal", "restored-local-terminal"} {
		b, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if c := terminalCapability(t, b); !c.Available || c.State != "ready" {
			b.Close()
			t.Fatalf("optional server readiness=%+v", c)
		}
		if err = b.ConfigureTerminal(true, true, "exec-a", "", cfg.Runtime.TerminalConfigHash); err != nil {
			b.Close()
			t.Fatalf("optional server enable=%v", err)
		}
		r.RequestID, r.ServerProfileID = requestID+"-remote", "server-a"
		j, err := b.Submit(context.Background(), r)
		if err != nil || j.State != "blocked" || j.Code != "terminal_selection_mismatch" {
			b.Close()
			t.Fatalf("empty binding switched to saved server=%+v %v", j, err)
		}
		r.RequestID, r.ServerProfileID, r.ExperimentID = requestID+"-experiment", "", "fixture-a"
		j, err = b.Submit(context.Background(), r)
		if err != nil || j.State != "rejected" || j.Code != "invalid_experiment_request" {
			b.Close()
			t.Fatalf("serverless experiment admitted=%+v %v", j, err)
		}
		r.RequestID, r.ExperimentID = requestID, ""
		j, err = b.Submit(context.Background(), r)
		if err != nil {
			b.Close()
			t.Fatal(err)
		}
		j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID != "" })
		p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "completed"}})
		waitRuntimeInactive(t, b, j.JobID)
		b.Close()
	}
	if p.count("turn/start") != 2 {
		t.Fatal("only local requests may start")
	}
}

func TestTerminalReadinessRequiresTheSelectedProfileAndServer(t *testing.T) {
	for _, tc := range []struct{ name, profile, server string }{{"missing execution", "missing", "server-a"}, {"missing server", "exec-a", "missing"}, {"unset execution", "", ""}} {
		t.Run(tc.name, func(t *testing.T) {
			p := &runtimePeer{approvalPolicy: "never"}
			cfg, _ := terminalBindingFixture(t, p)
			terminalSelection(t, &cfg, tc.profile, tc.server)
			b, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			if c := terminalCapability(t, b); c.Available || c.State == "ready" {
				t.Fatalf("other valid profile masked selected config=%+v", c)
			}
		})
	}
}

func TestTerminalPairChangeRejectsOldContinuationAndCanRestoreSelection(t *testing.T) {
	p := &runtimePeer{approvalPolicy: "never"}
	cfg, r := terminalBindingFixture(t, p)
	b, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	switcher, ok := any(b).(interface {
		ConfigureTerminal(bool, bool, string, string, string) error
	})
	if !ok {
		t.Fatal("terminal pair configuration is unavailable")
	}
	j, err := b.Submit(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID != "" })
	hash := refreshTerminalBinding(t, cfg)
	if err = switcher.ConfigureTerminal(true, true, "exec-a", "server-a", hash); err == nil || err.Error() != "terminal_runtime_busy" {
		t.Fatalf("same pair bypassed running guard=%v", err)
	}
	p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "completed"}})
	waitRuntimeInactive(t, b, j.JobID)
	if err = switcher.ConfigureTerminal(true, true, "exec-b", "server-b", hash); err != nil {
		t.Fatal(err)
	}
	next := model.CodexContinue{DeviceID: r.DeviceID, JobID: j.JobID, RequestID: "same-owned-job", Prompt: "continue"}
	if _, err = b.Continue(context.Background(), next); err == nil || err.Error() != "terminal_selection_mismatch" {
		t.Fatalf("old pair continued=%v", err)
	}
	if p.count("thread/resume") != 0 {
		t.Fatal("mismatched continuation reached Codex")
	}
	if err = switcher.ConfigureTerminal(true, true, "exec-a", "server-a", hash); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Continue(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID == "turn-2" })
	p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "completed"}})
	waitRuntimeInactive(t, b, j.JobID)
}

func TestTerminalPersistedConfigHashRejectsLiveAndRestartDrift(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(map[bool]string{false: "live", true: "restart"}[restart], func(t *testing.T) {
			p := &runtimePeer{approvalPolicy: "never"}
			cfg, r := terminalBindingFixture(t, p)
			b, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			store := connections.NewStore(cfg.Runtime.ConnectionsDir)
			c, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			changed := c.ExecutionProfiles[0]
			changed.PermissionProfile = ":danger-full-access"
			if _, err = store.UpsertExecution(changed); err != nil {
				t.Fatal(err)
			}
			if restart {
				b.Close()
				b, err = New(cfg)
				if err != nil {
					t.Fatal(err)
				}
				defer b.Close()
			}
			j, err := b.Submit(context.Background(), r)
			if err != nil || j.State != "blocked" || j.Code != "terminal_configuration_changed" {
				t.Fatalf("changed selected permission admitted=%+v %v", j, err)
			}
			if c := terminalCapability(t, b); c.Available || c.Code != "terminal_configuration_changed" {
				t.Fatalf("changed config ready=%+v", c)
			}
			if p.count("initialize") != 0 {
				t.Fatal("changed config reached Codex")
			}
		})
	}
}

func TestTerminalEnableRequiresSelectionAndPersistedHash(t *testing.T) {
	p := &runtimePeer{approvalPolicy: "never"}
	cfg, r := terminalBindingFixture(t, p)
	if err := json.Unmarshal([]byte(`{"TerminalConfigHash":""}`), cfg.Runtime); err != nil {
		t.Fatal(err)
	}
	b, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	j, err := b.Submit(context.Background(), r)
	if err != nil || j.State != "blocked" || j.Code != "needs_local_setup" {
		t.Fatalf("unbound restored enable admitted=%+v %v", j, err)
	}
	switcher, ok := any(b).(interface {
		ConfigureTerminal(bool, bool, string, string, string) error
	})
	if !ok {
		t.Fatal("terminal pair configuration unavailable")
	}
	for _, tc := range []struct{ profile, server, hash string }{{"", "", ""}, {"exec-a", "server-a", ""}, {"exec-a", "server-a", "bogus"}} {
		if err = switcher.ConfigureTerminal(true, true, tc.profile, tc.server, tc.hash); err == nil {
			t.Fatal("invalid local terminal enable accepted")
		}
	}
}

func TestTerminalConfigurationDriftCannotTurnUnknownRecoveryIntoStopped(t *testing.T) {
	p := &runtimePeer{approvalPolicy: "never"}
	cfg, r := terminalBindingFixture(t, p)
	b, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	j, err := b.Submit(context.Background(), r)
	if err != nil {
		b.Close()
		t.Fatal(err)
	}
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID != "" })
	p.mu.Lock()
	conn := p.conn
	p.mu.Unlock()
	conn.Close()
	waitRuntimeInactive(t, b, j.JobID)
	b.Close()
	store := connections.NewStore(cfg.Runtime.ConnectionsDir)
	c, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	pChanged := c.ExecutionProfiles[0]
	pChanged.ProjectDir = t.TempDir()
	if _, err = store.UpsertExecution(pChanged); err != nil {
		t.Fatal(err)
	}
	b, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	waitRuntimeInactive(t, b, j.JobID)
	j, err = b.Job(r.DeviceID, j.JobID)
	if err != nil || j.State != "outcome_unknown" || j.Code != "profile_changed" {
		t.Fatalf("configuration edit implied stop=%+v %v", j, err)
	}
	if err = b.ConfigureTerminal(true, false, "", "", ""); err == nil || err.Error() != "terminal_runtime_busy" {
		t.Fatalf("unknown recovery allowed mode switch=%v", err)
	}
	if p.count("turn/start") != 1 || p.count("thread/read") != 0 {
		t.Fatal("configuration drift replayed or redirected recovery")
	}
}

func TestTerminalConfigHashNormalizesWithoutMutatingInput(t *testing.T) {
	cfg := connections.Config{Version: 1, ExecutionProfiles: []connections.ExecutionProfile{{ID: "profile", Name: "project", ProjectDir: t.TempDir(), Execution: "terminal", PermissionProfile: ":read-only", ApprovalPolicy: "never"}}}
	before, _ := json.Marshal(cfg)
	actual := TerminalConfigHash(cfg)
	after, _ := json.Marshal(cfg)
	if string(before) != string(after) {
		t.Fatal("hash changed caller configuration")
	}
	cfg.Normalize()
	raw, _ := json.Marshal(cfg)
	expected := sha256.Sum256(raw)
	if actual != hex.EncodeToString(expected[:]) {
		t.Fatal("hash differs from normalized persisted JSON")
	}
	cfg.ExecutionProfiles[0].PermissionProfile = ":danger-full-access"
	if TerminalConfigHash(cfg) == actual {
		t.Fatal("permission change did not invalidate binding")
	}
}

func TestNeverPolicyUnboundApprovalInterruptsOnlyOwnedTurn(t *testing.T) {
	for _, params := range []any{map[string]any{}, map[string]any{"threadId": "owned-thread"}, map[string]any{"threadId": 23}} {
		t.Run("unbound", func(t *testing.T) {
			p := &runtimePeer{approvalPolicy: "never"}
			cfg, r := terminalBindingFixture(t, p)
			b, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			j, _ := b.Submit(context.Background(), r)
			j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID != "" })
			p.send(map[string]any{"id": "unbound", "method": "item/permissions/requestApproval", "params": params})
			j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.CancelRequested && p.count("turn/interrupt") > 0 })
			if j.Code != "needs_local_setup" || len(j.Runtime.Approvals) > 0 || p.count("") > 0 {
				t.Fatalf("unbound request became review=%+v", j)
			}
			interrupted := peerParams(t, p, "turn/interrupt", 0)
			if interrupted["threadId"] != j.ThreadID || interrupted["turnId"] != j.TurnID {
				t.Fatalf("unbound request changed stop target=%v", interrupted)
			}
			p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "interrupted"}})
			waitRuntimeInactive(t, b, j.JobID)
		})
	}
}
