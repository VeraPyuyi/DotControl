package driver

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var errEmptyResponse = errors.New("empty_response")
var errToolResponse = errors.New("tool_error")

type callFailure struct {
	Operation string         `json:"operation"`
	Kind      string         `json:"kind"`
	RPCCode   int64          `json:"rpc_code,omitempty"`
	At        time.Time      `json:"at"`
	Origin    string         `json:"origin"`
	Native    *nativeFailure `json:"native,omitempty"`
}

type diagnosticState struct {
	connected            bool
	closed               bool
	reconnectionRequired bool
	lastFailure          *callFailure
	origin               string
}

// Diagnostic reports the last known transport state, not a live GUI probe.
// It uses a separate lock so status remains available during a native call.
// No request arguments, response content, or arbitrary error strings escape.
func (d *Cua) Diagnostic() map[string]any {
	d.diagnosticMu.Lock()
	defer d.diagnosticMu.Unlock()
	status := map[string]any{
		"diagnostic_build":      NativeDiagnosticBuild,
		"connected":             d.diagnostic.connected && !d.diagnostic.closed,
		"reconnection_required": d.diagnostic.reconnectionRequired || d.diagnostic.closed,
	}
	status["failure_journal_enabled"], status["failure_journal_write_failed"] = failureJournalStatus()
	if d.diagnostic.lastFailure != nil {
		status["last_failure"] = *d.diagnostic.lastFailure
	}
	return status
}

// Distinguish the release's existing startup/repair probes from plugin reads.
func (d *Cua) SetDiagnosticOrigin(origin string) {
	if origin != "startup_probe" && origin != "desktop_request" {
		origin = "other"
	}
	d.diagnosticMu.Lock()
	d.diagnostic.origin = origin
	d.diagnosticMu.Unlock()
}

func (d *Cua) recordSuccess() {
	d.diagnosticMu.Lock()
	defer d.diagnosticMu.Unlock()
	if !d.diagnostic.reconnectionRequired && !d.diagnostic.closed {
		d.diagnostic.connected = true
	}
}

func (d *Cua) recordFailure(operation string, err error, dispatched, reconnect bool, native ...nativeFailure) {
	switch operation {
	case "list_apps", "list_windows", "get_window_state", "click", "type_text", "set_value", "press_key", "hotkey", "scroll", "drag", "end_session":
	default:
		operation = "other"
	}
	failure := callFailure{Operation: operation, Kind: "transport_error", At: time.Now().UTC()}
	d.diagnosticMu.Lock()
	failure.Origin = d.diagnostic.origin
	d.diagnosticMu.Unlock()
	if failure.Origin == "" {
		failure.Origin = "desktop_request"
	}
	if len(native) == 1 {
		failure.Native = &native[0]
	}
	var rpcError *jsonrpc.Error
	switch {
	case errors.Is(err, context.Canceled):
		failure.Kind = "request_cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		failure.Kind = "request_deadline"
	case errors.Is(err, mcp.ErrConnectionClosed), errors.Is(err, io.EOF), errors.Is(err, io.ErrClosedPipe):
		failure.Kind = "connection_closed"
	case errors.As(err, &rpcError):
		failure.Kind = "rpc_error"
		if rpcError.Code == -32700 || rpcError.Code >= -32603 && rpcError.Code <= -32600 || rpcError.Code >= -32099 && rpcError.Code <= -32000 {
			failure.RPCCode = rpcError.Code
		}
	case errors.Is(err, errEmptyResponse):
		failure.Kind = "empty_response"
	case errors.Is(err, errToolResponse):
		failure.Kind = "tool_error"
	}
	// Persist before cleanup, wrapping, or automatic reconnection can replace it.
	persistFailure(failure)
	d.diagnosticMu.Lock()
	defer d.diagnosticMu.Unlock()
	// Preserve the cause of a latched transport failure across blocked calls.
	if d.diagnostic.reconnectionRequired {
		return
	}
	// Cleanup is useful in the journal, but must not hide the failed request.
	if operation == "end_session" && d.diagnostic.lastFailure != nil {
		return
	}
	d.diagnostic.lastFailure = &failure
	if dispatched {
		d.diagnostic.connected = false
	}
	d.diagnostic.reconnectionRequired = reconnect
}
