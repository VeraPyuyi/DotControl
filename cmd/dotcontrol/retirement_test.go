package main

import (
	"context"
	"encoding/json"
	"github.com/dotcontrol/dotcontrol/internal/onboard"
	"os"
	"path/filepath"
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

func TestRetiredConnectionFieldPresenceRejectsBeforeStoredConfiguration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "connections", "connections.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	legacy := []byte(`{"legacy_configuration":"must remain byte-for-byte untouched"}`)
	if err := os.WriteFile(path, legacy, 0600); err != nil {
		t.Fatal(err)
	}
	owner := &runtimeOwner{dir: dir}
	for _, body := range []string{
		`{"operation":"list","server":null}`,
		`{"operation":"save_execution","Server_Profile_Id":""}`,
		`{"operation":"enable_terminal","PUBLIC_KEY_PATH":null}`,
		`{"operation":"save_codex","Authorization_Id":""}`,
	} {
		var request onboard.ConnectionsRequest
		if err := json.Unmarshal([]byte(body), &request); err != nil {
			t.Fatal(err)
		}
		if _, err := owner.Connections(context.Background(), request); err == nil || err.Error() != "feature_retired" {
			t.Fatalf("retired field %s reached local configuration: %v", body, err)
		}
		stored, err := os.ReadFile(path)
		if err != nil || string(stored) != string(legacy) {
			t.Fatalf("retired request modified legacy configuration: %v", err)
		}
	}
}
