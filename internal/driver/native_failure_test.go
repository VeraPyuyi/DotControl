package driver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestNativeErrorCapturedAtRealAdapterBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "boundary.jsonl")
	closeJournal, err := OpenFailureJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeJournal()
	fake := &fakeCaller{respond: func(name string, args map[string]any) (*mcp.CallToolResult, error) {
		return refusal("tool_invocation_failed", 1, "session has ended; tool call 'list_apps' was rejected. private-session"), nil
	}}
	d := &Cua{client: fake}
	if _, err := d.Apps(context.Background()); err == nil || err.Error() != "app_discovery_failed" {
		t.Fatal("adapter behavior changed")
	}
	if len(fake.calls) != 1 {
		t.Fatal("adapter retried or reached list_windows")
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), `"code":"tool_invocation_failed"`) || !strings.Contains(string(data), `"reason":"session_ended"`) {
		t.Fatal("native diagnosis lost before wrapping")
	}
	if strings.Contains(string(data), "private-session") {
		t.Fatal("session text leaked")
	}
}

func refusal(code string, exit int, message string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, StructuredContent: map[string]any{"code": code, "exit_code": exit},
		Content: []mcp.Content{&mcp.TextContent{Text: message}}}
}

func TestSafeNativeReasonsAndSecretSuppression(t *testing.T) {
	secret := "sk-private-secret /private/user/path window=private-title session=private-label"
	cases := []struct {
		name, code, message, reason, sdk string
		exit                             int
		hint                             bool
	}{
		{"ended", "tool_invocation_failed", "session has ended; tool call 'list_apps' was rejected. " + secret, "session_ended", "", 1, true},
		{"authorization", "tool_invocation_failed", "Permission denied: " + secret, "authorization_denied", "", 1, true},
		{"sdk_arguments", "tool_invocation_failed", "invalid arguments for list_apps: " + secret, "sdk_failure", "invalid_arguments", 1, true},
		{"sdk_tool", "tool_invocation_failed", "list_apps failed: " + secret, "sdk_failure", "tool", 1, true},
		{"native_auth", "authorization_revoked", secret, "authorization_refusal", "", 1, false},
		{"auth_host_failure", "authorization_host_failed", secret, "authorization_host_failure", "", 1, false},
		{"auth_expired", "authorization_expired", secret, "authorization_expired", "", 1, false},
		{"generic_wrapper", "tool_invocation_failed", secret, "unclassified", "", 1, false},
		{"wrong_exit", "tool_invocation_failed", "Permission denied: " + secret, "unclassified", "", 77, false},
		{"not_prefix", "tool_invocation_failed", secret + " Permission denied: ignored", "unclassified", "", 1, false},
		{"output", "tool_output_invalid", secret, "output_contract_failure", "", 1, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := safeNativeFailure("list_apps", refusal(c.code, c.exit, c.message))
			if got.Code != c.code || got.Reason != c.reason || got.SDKKind != c.sdk || got.ReasonHintOnly != c.hint {
				t.Fatalf("unexpected safe classification: %#v", got)
			}
			b, _ := json.Marshal(got)
			for _, forbidden := range []string{"sk-private-secret", "/private/user/path", "private-title", "private-label"} {
				if strings.Contains(string(b), forbidden) {
					t.Fatal("private data escaped classification")
				}
			}
		})
	}
}

func TestNativeMetadataFailClosed(t *testing.T) {
	unknown := refusal("sk-secret-that-looks-like-a-code", 1, "Permission denied: secret")
	got := safeNativeFailure("list_apps", unknown)
	if !got.CodePresent || got.Code != "unclassified" || got.Reason != "unclassified" {
		t.Fatal("unknown metadata was trusted")
	}
	conflict := refusal("tool_invocation_failed", 1, "Permission denied: secret")
	conflict.StructuredContent.(map[string]any)["refusal"] = map[string]any{"code": "authorization_revoked"}
	got = safeNativeFailure("list_apps", conflict)
	if !got.MetadataConflict || got.Reason != "ambiguous" || got.Code != "unclassified" {
		t.Fatal("conflicting metadata was trusted")
	}
	duplicate := &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: `{"code":"tool_invocation_failed","code":"authorization_revoked","exit_code":1}`}}}
	if got := safeNativeFailure("list_apps", duplicate); got.CodePresent {
		t.Fatal("duplicate keys accepted")
	}
	hidden := refusal("tool_invocation_failed", 1, "unclassified")
	hidden.StructuredContent.(map[string]any)["invalid_output"] = map[string]any{"code": "authorization_revoked", "apps": []string{"private-app"}}
	if got := safeNativeFailure("list_apps", hidden); got.Code != "tool_invocation_failed" || got.Reason != "unclassified" {
		t.Fatal("private payload traversed")
	}
	oversized := refusal("tool_invocation_failed", 1, "Permission denied: "+strings.Repeat("s", protocolLimit))
	if got := safeNativeFailure("list_apps", oversized); !got.MetadataLimited || got.Reason != "unclassified" {
		t.Fatal("oversized text accepted")
	}
	misleading := refusal("tool_invocation_failed", 1, "session has ended; tool call 'list_windows' was rejected. secret")
	if got := safeNativeFailure("list_apps", misleading); got.Reason != "unclassified" {
		t.Fatal("wrong tool template trusted")
	}
	conflictingText := refusal("tool_invocation_failed", 1, "Permission denied: secret")
	conflictingText.Content = append(conflictingText.Content, &mcp.TextContent{Text: "list_apps failed: another-secret"})
	if got := safeNativeFailure("list_apps", conflictingText); got.Reason != "ambiguous" {
		t.Fatal("conflicting text hints trusted")
	}
}

func TestFailureJournalPersistsBeforeReconnectAndWithoutSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "safe.jsonl")
	closeJournal, err := OpenFailureJournal(path)
	if err != nil {
		t.Fatal("cannot create test journal")
	}
	defer closeJournal()
	d := &Cua{}
	d.SetDiagnosticOrigin("startup_probe")
	native := safeNativeFailure("list_apps", refusal("tool_invocation_failed", 1, "invalid arguments for list_apps: sk-private-secret"))
	d.recordFailure("list_apps", errToolResponse, true, true, native)
	// A later connection and cleanup must not erase the first record.
	other := &Cua{}
	other.SetDiagnosticOrigin("desktop_request")
	other.recordFailure("list_windows", errToolResponse, true, false, safeNativeFailure("list_windows", refusal("authorization_revoked", 1, "private-title")))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("journal not readable")
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("wrong record count %d", len(lines))
	}
	for _, line := range lines {
		var entry map[string]any
		if json.Unmarshal([]byte(line), &entry) != nil || entry["at"] == nil || entry["diagnostic_build"] != NativeDiagnosticBuild {
			t.Fatal("invalid durable record")
		}
	}
	if !strings.Contains(lines[0], `"origin":"startup_probe"`) || !strings.Contains(lines[1], `"origin":"desktop_request"`) {
		t.Fatal("origins not distinguished")
	}
	if strings.Contains(string(data), "sk-private-secret") || strings.Contains(string(data), "private-title") {
		t.Fatal("raw private response persisted")
	}
	if err := validatePrivateJournalFile(failureJournal.file); err != nil {
		t.Fatal("journal is not private")
	}
}

func TestFailureJournalRejectsLinksAndPublicFiles(t *testing.T) {
	dir := t.TempDir()
	public := filepath.Join(dir, "public.jsonl")
	if err := os.WriteFile(public, []byte("sentinel"), 0o644); err != nil {
		t.Fatal(err)
	}
	makePublicJournal(t, public)
	if closeJournal, err := OpenFailureJournal(public); err == nil {
		closeJournal()
		t.Fatal("public file accepted")
	}
	link := filepath.Join(dir, "linked.jsonl")
	if err := os.Symlink(public, link); err != nil {
		if runtime.GOOS != "windows" {
			t.Fatal(err)
		}
	} else if closeJournal, err := OpenFailureJournal(link); err == nil {
		closeJournal()
		t.Fatal("symlink accepted")
	}
	hard := filepath.Join(dir, "hard.jsonl")
	if err := os.Link(public, hard); err != nil {
		t.Fatal(err)
	}
	if closeJournal, err := OpenFailureJournal(hard); err == nil {
		closeJournal()
		t.Fatal("hardlink accepted")
	}
	data, _ := os.ReadFile(public)
	if string(data) != "sentinel" {
		t.Fatal("existing file was altered")
	}
}

func TestFailureJournalReopensPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private.jsonl")
	for range 2 {
		closeJournal, err := OpenFailureJournal(path)
		if err != nil {
			t.Fatal(err)
		}
		persistFailure(callFailure{Operation: "list_apps", Kind: "tool_error"})
		closeJournal()
	}
	data, err := os.ReadFile(path)
	if err != nil || strings.Count(string(data), "\n") != 2 {
		t.Fatal("append failed")
	}
}

func TestCleanupDoesNotReplaceRequestFailure(t *testing.T) {
	d := &Cua{}
	d.recordFailure("list_apps", errToolResponse, true, false)
	d.recordFailure("end_session", errToolResponse, true, false)
	data, _ := json.Marshal(d.Diagnostic())
	if strings.Contains(string(data), "end_session") || !strings.Contains(string(data), "list_apps") {
		t.Fatal("cleanup hid cause")
	}
}
