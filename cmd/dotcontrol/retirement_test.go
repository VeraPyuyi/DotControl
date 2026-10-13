package main

import (
	"context"
	"github.com/dotcontrol/dotcontrol/internal/onboard"
	"testing"
)

func TestExperimentCLIAlwaysRetired(t *testing.T) {
	for _, args := range [][]string{nil, {"submit"}, {"status", "--profile", "old", "--experiment-id", "old"}, {"stop"}, {"unknown"}} {
		if err := experimentCLI(context.Background(), t.TempDir(), args); err == nil || err.Error() != "feature_retired" {
			t.Fatalf("%v: %v", args, err)
		}
	}
}

func TestTerminalCheckRejectsExplicitEmptyServerFlag(t *testing.T) {
	for _, entry := range []func(context.Context, string, []string) error{terminalCheckCLI, terminalCheckCommand} {
		if err := entry(context.Background(), "nonexistent-state", []string{"--server-profile", ""}); err == nil || err.Error() != "feature_retired" {
			t.Fatalf("empty retired CLI flag: %v", err)
		}
	}
}
func TestRetiredConnectionOperationsNeverTouchRuntime(t *testing.T) {
	owner := &runtimeOwner{}
	for _, op := range []string{"save_server", "check_ssh", "authorize_prepare", "authorize_start", "authorize_status", "pick_public_key", "pick_identity"} {
		_, err := owner.Connections(context.Background(), onboard.ConnectionsRequest{Operation: op})
		if err == nil || err.Error() != "feature_retired" {
			t.Fatalf("%s: %v", op, err)
		}
	}
}
