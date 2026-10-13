package main

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/dotcontrol/dotcontrol/internal/codexbridge"
)

func TestTerminalRPCFailureIsClassifiedWithoutPrivateMessage(t *testing.T) {
	for _, tc := range []struct{ message, want string }{
		{"windows sandbox cannot enforce split writable root sets directly; refusing to run unsandboxed PRIVATE", "terminal_sandbox_layout_unsupported"},
		{"sandbox cannot enforce split writable roots PRIVATE", "terminal_sandbox_layout_unsupported"},
		{"PRIVATE unknown error", "terminal_probe_failed"},
	} {
		raw, _ := json.Marshal(map[string]any{"code": -32603, "message": tc.message})
		var rpc codexbridge.RPCError
		if err := json.Unmarshal(raw, &rpc); err != nil {
			t.Fatal(err)
		}
		if got := terminalProbeErrorCode(fmt.Errorf("wrapped: %w", &rpc)); got != tc.want {
			t.Fatal(got)
		}
		encoded, _ := json.Marshal(&rpc)
		if strings.Contains(rpc.Error()+string(encoded), "PRIVATE") {
			t.Fatal("upstream text leaked")
		}
	}
}

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
