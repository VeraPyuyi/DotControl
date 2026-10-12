package main

import (
	"context"
	"github.com/dotcontrol/dotcontrol/internal/connections"
	"slices"
	"strings"
	"testing"
)

func TestTerminalProbeNeedsNonceAndZeroExit(t *testing.T) {
	for _, tc := range []struct {
		exit int
		out  string
		want string
	}{
		{0, `{"available":true,"code":"terminal_ready","nonce":"expected"}`, "terminal_ready"},
		{0, `{"available":true,"code":"terminal_ready","nonce":"wrong"}`, "terminal_probe_unconfirmed"},
		{1, `{"available":true,"code":"terminal_ready","nonce":"expected"}`, "terminal_probe_unconfirmed"},
		{0, "", "terminal_probe_unconfirmed"},
		{1, `{"available":false,"code":"ssh_auth_failed","nonce":"expected"}`, "ssh_auth_failed"},
		{1, `{"available":false,"code":"PRIVATE MATERIAL","nonce":"expected"}`, "terminal_probe_failed"},
	} {
		got := terminalProbeResult(tc.exit, tc.out, "expected")
		if got.Code != tc.want {
			t.Fatalf("%q: %+v", tc.out, got)
		}
	}
}

func TestBasicTerminalUsesWorkspaceAndNoEmptyNativeArguments(t *testing.T) {
	p := basicTerminalProfile(t.TempDir())
	if p.PermissionProfile != ":workspace" || p.ApprovalPolicy != "never" || p.Execution != "terminal" || p.Presentation != "visible" {
		t.Fatalf("unsafe default: %+v", p)
	}
	a := terminalCheckArgs("helper", "state", "", "nonce")
	if slices.Contains(a, "") || slices.Contains(a, "--server-profile") {
		t.Fatalf("Windows would drop an argument: %v", a)
	}
}

func TestTerminalHelperDoesNotSubmitExperiments(t *testing.T) {
	dir, cwd := t.TempDir(), t.TempDir()
	store := connections.NewStore(dir)
	_, err := store.UpsertServer(connections.ServerProfile{ID: "server", Name: "Test", SSHAlias: "example.invalid", RemoteDir: "/example"})
	if err != nil {
		t.Fatal(err)
	}
	called := false
	result := runTerminalHelper(context.Background(), dir, cwd, "server", strings.Repeat("a", 64), func(ctx context.Context, p connections.ServerProfile, opts connections.PreflightOptions) connections.Status {
		called = true
		return connections.Status{Available: true, Code: "ssh_ready"}
	})
	if !called || !result.Available || result.Code != "terminal_ready" {
		t.Fatalf("%+v", result)
	}
}

func TestBasicTerminalDoesNotRequireSSHOrDeviceJournalAccess(t *testing.T) {
	r := runTerminalHelper(context.Background(), "nonexistent-connections", t.TempDir(), "", "nonce", func(context.Context, connections.ServerProfile, connections.PreflightOptions) connections.Status {
		t.Fatal("basic installation must not connect SSH")
		return connections.Status{}
	})
	if !r.Available || r.Code != "terminal_ready" {
		t.Fatalf("%+v", r)
	}
}
