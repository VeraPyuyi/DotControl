package codexbridge

import (
	"context"
	"testing"

	"github.com/dotcontrol/dotcontrol/internal/connections"
	"github.com/dotcontrol/dotcontrol/internal/model"
)

func requireTerminalOptIn(t *testing.T, cfg *Config) {
	t.Helper()
	cfg.Runtime.TerminalOptInRequired = true
}

func TestProductTerminalOptInPreventsLegacyProfileAutoEnable(t *testing.T) {
	for _, policy := range []string{"", "on-request", "never"} {
		t.Run(policy, func(t *testing.T) {
			p := &runtimePeer{approvalPolicy: policy}
			cfg, r := runtimeFixture(t, p)
			terminalPolicy(t, cfg, policy)
			requireTerminalOptIn(t, &cfg)
			b, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			j, err := b.Submit(context.Background(), r)
			if err != nil || j.State != "blocked" || j.Code != "terminal_disabled" {
				t.Fatalf("legacy profile auto-enabled=%+v %v", j, err)
			}
			if c := terminalCapability(t, b); c.Available || c.State != "disabled" || c.Code != "terminal_disabled" {
				t.Fatalf("legacy capability enabled=%+v", c)
			}
			// A read-only recovery connection cannot enable terminal delegation.
			b.mu.Lock()
			b.runtime.connections = 1
			b.mu.Unlock()
			caps := b.Capabilities()
			if !caps.CodexConnection.Available || caps.CodexDelegation.Available {
				t.Fatalf("recovery connection bypassed opt-in=%+v", caps)
			}
			stored, err := connections.NewStore(cfg.Runtime.ConnectionsDir).Load()
			if err != nil || stored.ExecutionProfiles[0].ApprovalPolicy != policy {
				t.Fatal("legacy approval policy changed")
			}
			if p.count("initialize") != 0 {
				t.Fatal("legacy product profile contacted Codex")
			}
		})
	}
}

func TestProductTerminalOptInRequiresModeAndEnableTogether(t *testing.T) {
	for _, tc := range []struct{ only, enabled bool }{{false, false}, {false, true}, {true, false}} {
		p := &runtimePeer{approvalPolicy: "never"}
		cfg, r := runtimeFixture(t, p)
		terminalPolicy(t, cfg, "never")
		requireTerminalOptIn(t, &cfg)
		cfg.Runtime.TerminalOnly, cfg.Runtime.TerminalEnabled = tc.only, tc.enabled
		b, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		j, err := b.Submit(context.Background(), r)
		if err != nil || j.Code != "terminal_disabled" {
			b.Close()
			t.Fatalf("partial opt-in admitted=%+v %v", j, err)
		}
		if c := terminalCapability(t, b); c.Available || c.State != "disabled" {
			b.Close()
			t.Fatalf("partial opt-in ready=%+v", c)
		}
		b.Close()
	}
}

func TestProductTerminalOptInAllowsExplicitLocalEnable(t *testing.T) {
	p := &runtimePeer{approvalPolicy: "never"}
	cfg, r := runtimeFixture(t, p)
	terminalPolicy(t, cfg, "never")
	requireTerminalOptIn(t, &cfg)
	b, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err = b.ConfigureTerminal(true, true, "exec-a", "", cfg.Runtime.TerminalConfigHash); err != nil {
		t.Fatal(err)
	}
	if c := terminalCapability(t, b); !c.Available || c.State != "ready" {
		t.Fatalf("explicit opt-in not ready=%+v", c)
	}
	j, err := b.Submit(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID != "" })
	p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "completed"}})
	waitRuntimeInactive(t, b, j.JobID)
}

func TestProductTerminalOptInBlocksRestoredLegacyContinuation(t *testing.T) {
	p := &runtimePeer{}
	cfg, r := runtimeFixture(t, p)
	b, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	j, _ := b.Submit(context.Background(), r)
	j = waitRuntime(t, b, j.JobID, func(j model.CodexJob) bool { return j.TurnID != "" })
	p.notification("turn/completed", map[string]any{"threadId": j.ThreadID, "turn": map[string]any{"id": j.TurnID, "status": "completed"}})
	waitRuntimeInactive(t, b, j.JobID)
	b.Close()
	requireTerminalOptIn(t, &cfg)
	b, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	_, err = b.Continue(context.Background(), model.CodexContinue{DeviceID: r.DeviceID, JobID: j.JobID, RequestID: "upgrade-continuation", Prompt: "continue"})
	if err == nil || err.Error() != "terminal_disabled" {
		t.Fatalf("upgrade silently re-enabled old thread=%v", err)
	}
	if p.count("thread/resume") != 0 || p.count("turn/start") != 1 {
		t.Fatal("unapproved upgrade resumed Codex")
	}
}
