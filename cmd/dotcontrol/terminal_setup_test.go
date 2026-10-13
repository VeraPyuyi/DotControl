package main

import (
	"context"
	"slices"
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
		{1, `{"available":false,"code":"feature_retired","nonce":"expected"}`, "feature_retired"},
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

func TestTerminalHelperRetiresServerBeforeFilesystemAccess(t *testing.T) {
	r := runTerminalHelper(context.Background(), "not-a-directory", "legacy", "nonce")
	if r.Available || r.Code != "feature_retired" {
		t.Fatalf("%+v", r)
	}
}
func TestBasicTerminalDoesNotRequireSSHOrDeviceJournalAccess(t *testing.T) {
	r := runTerminalHelper(context.Background(), t.TempDir(), "", "nonce")
	if !r.Available || r.Code != "terminal_ready" {
		t.Fatalf("%+v", r)
	}
}
