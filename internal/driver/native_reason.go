package driver

import "strings"

// These are content-derived hints, not authoritative native stage/class fields.
// Only the fixed 0.34.1 templates at serve.rs, policy.rs, SDK lib.rs and
// sdk_adapter.rs are inspected. Variable suffixes are never copied or logged.
type failurePathHint struct {
	path, sdkKind string
}

func (h *failurePathHint) selectHint(path, sdkKind string) {
	if h.path == "ambiguous" {
		return
	}
	if h.path != "" && (h.path != path || h.sdkKind != sdkKind) {
		h.path, h.sdkKind = "ambiguous", ""
		return
	}
	h.path, h.sdkKind = path, sdkKind
}

func (h *failurePathHint) inspect(tool, text string) {
	text = strings.TrimSpace(text)
	// Never scan embedded JSON, suffixes, line interiors or variable paths.
	if tool == "list_apps" || tool == "list_windows" {
		if strings.HasPrefix(text, "session has ended; tool call '"+tool+"' was rejected. ") {
			h.selectHint("session_ended", "")
			return
		}
		for _, item := range []struct{ prefix, kind string }{
			{"invalid arguments for " + tool + ": ", "invalid_arguments"},
			{tool + " failed: ", "tool"},
			{tool + " returned invalid SDK result JSON: ", "adapter_result_json_invalid"},
		} {
			if strings.HasPrefix(text, item.prefix) {
				h.selectHint("sdk_failure", item.kind)
				return
			}
		}
	}
	for _, item := range []struct{ prefix, path, kind string }{
		{"Permission denied: ", "authorization_denied", ""},
		{"Policy evaluation error: ", "authorization_evaluation_failed", ""},
		{"Policy loading error: ", "authorization_loading_failed", ""},
		{"invalid SDK configuration: ", "sdk_failure", "configuration"},
		{"daemon transport error on ", "sdk_failure", "transport"},
		{"invalid daemon response: ", "sdk_failure", "protocol"},
		{"runtime_already_exists: one direct Cua Driver runtime is already active in this process", "sdk_failure", "runtime_conflict"},
		{"private worker error: ", "sdk_failure", "worker"},
		{"remote Driver error: ", "sdk_failure", "remote"},
		{"driver action was interrupted (NotStarted): ", "sdk_failure", "action_interrupted"},
		{"driver action was interrupted (Completed): ", "sdk_failure", "action_interrupted"},
		{"driver action was interrupted (Unknown): ", "sdk_failure", "action_interrupted"},
	} {
		if strings.HasPrefix(text, item.prefix) {
			h.selectHint(item.path, item.kind)
			return
		}
	}
	if text == "the Cua Driver SDK has been shut down" {
		h.selectHint("sdk_failure", "shutdown")
	}
}
