package driver

import (
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Exact public literals reviewed against cua-driver-rs-v0.34.1. Arbitrary
// snake_case identifiers, private messages, paths and session labels are refused.
var driverCodes = enumWords("tool_invocation_failed tool_output_invalid authorization_suspended authorization_revoked authorization_declined authorization_canceled authorization_scope_mismatch authorization_expired authorization_host_failed bounded_resource_outside_manifest protected_resource_scope_invalid protected_resource_scope_stale foreign_process_termination_denied unbounded_operation_requires_unrestricted os_permission_prompt_requires_trusted_host input_busy runtime_output_key_collision action_outcome_mismatch typed_output_mismatch config_key_retired invalid_arguments desktop_scope_disabled stale_element_token screenshot_context_missing ambiguous_window_target window_target_not_found permission_denied")
var driverStages = enumWords("authorization permissions permission_gate session session_validation transport dispatch tool_execution runtime initialization list_apps list_windows")
var driverClasses = enumWords("permission_denied authorization session_error transport_error timeout validation_error invalid_argument internal_error unavailable protocol execution")

func enumWords(text string) map[string]bool {
	m := make(map[string]bool)
	for _, word := range strings.Fields(text) {
		m[word] = true
	}
	return m
}

type nativeFailure struct {
	Code             string `json:"code,omitempty"`
	CodePresent      bool   `json:"code_present"`
	NumericCode      *int   `json:"numeric_code,omitempty"`
	ExitCode         *int   `json:"exit_code,omitempty"`
	Stage            string `json:"stage,omitempty"`
	Class            string `json:"class,omitempty"`
	MetadataSource   string `json:"metadata_source"`
	Reason           string `json:"reason"`
	ReasonSource     string `json:"reason_source"`
	ReasonHintOnly   bool   `json:"reason_hint_only"`
	SDKKind          string `json:"sdk_kind,omitempty"`
	MetadataConflict bool   `json:"metadata_conflict"`
	MetadataLimited  bool   `json:"metadata_limited"`
}

func safeNativeFailure(tool string, result *mcp.CallToolResult) nativeFailure {
	n := nativeFailure{Reason: "unclassified", ReasonSource: "none", MetadataSource: "none"}
	if result == nil || !result.IsError {
		return n
	}
	m := protocolMetadata{}
	m.read(result.StructuredContent, 0, "structured")
	hint := failurePathHint{}
	pending := false
	budget := protocolLimit
	for index, content := range result.Content {
		if index >= 4 || budget <= 0 {
			m.truncated = true
			break
		}
		text, ok := content.(*mcp.TextContent)
		if !ok {
			continue
		}
		if len(text.Text) > budget {
			m.truncated = true
			continue // Do not classify truncated prefixes or partial JSON.
		}
		budget -= len(text.Text)
		m.read(text.Text, 0, "text_json")
		hint.inspect(tool, text.Text)
		pending = pending || strings.HasPrefix(strings.TrimSpace(text.Text), "permissions_pending:")
	}
	n.Code, n.CodePresent, n.NumericCode, n.ExitCode = m.code, m.codePresent, m.numeric, m.exitCode
	n.Stage, n.Class = m.stage, m.class
	n.MetadataConflict, n.MetadataLimited = m.conflict, m.truncated
	if m.source != "" {
		n.MetadataSource = m.source
	}
	if m.codePresent && m.code == "" && m.numeric == nil {
		n.Code = "unclassified"
	}
	if m.conflict {
		n.Code, n.NumericCode, n.Stage, n.Class, n.SDKKind = "unclassified", nil, "", "", ""
		n.Reason = "ambiguous"
		return n
	}
	// Code-derived categories are distinct from hints based on fixed message
	// templates. A wrapper code alone never establishes an underlying reason.
	switch {
	case m.code == "authorization_host_failed":
		n.Reason, n.ReasonSource = "authorization_host_failure", "native_code"
	case m.code == "authorization_expired" || m.code == "authorization_scope_mismatch" || m.code == "authorization_suspended":
		n.Reason, n.ReasonSource = m.code, "native_code"
	case m.code == "authorization_revoked" || m.code == "authorization_declined" || m.code == "authorization_canceled" || m.code == "permission_denied":
		n.Reason, n.ReasonSource = "authorization_refusal", "native_code"
	case m.code == "invalid_arguments":
		n.Reason, n.ReasonSource = "argument_validation", "native_code"
	case m.code == "tool_output_invalid" || m.code == "typed_output_mismatch":
		n.Reason, n.ReasonSource = "output_contract_failure", "native_code"
	case m.code == "tool_invocation_failed" && m.exitCode != nil && *m.exitCode == 1 && hint.path != "":
		n.Reason, n.SDKKind, n.ReasonSource, n.ReasonHintOnly = hint.path, hint.sdkKind, "verified_text_template", true
	case m.code == "tool_invocation_failed" && m.exitCode != nil && *m.exitCode == 77 && pending:
		n.Reason, n.ReasonSource, n.ReasonHintOnly = "permissions_pending", "verified_text_template", true
	}
	return n
}
