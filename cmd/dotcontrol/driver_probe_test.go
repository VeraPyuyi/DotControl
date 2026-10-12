package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/dotcontrol/dotcontrol/internal/driver"
	"testing"
)

type probeFixture struct {
	err    error
	closed bool
}

func TestDriverStatusRetainsFailureAcrossReplacement(t *testing.T) {
	failure := map[string]any{"operation": "list_apps", "kind": "tool_error"}
	o := &runtimeOwner{driverDiagnostic: map[string]any{"connected": false, "last_failure": failure}}
	o.driverStatus()
	o.driverDiagnostic = nil
	o.native = &driver.Cua{}
	status := o.driverStatus()
	if status["last_failure"].(map[string]any)["operation"] != "list_apps" || status["connected"] != false {
		t.Fatal("historical failure lost or confused with connection")
	}
}

func (d *probeFixture) Apps(context.Context) (json.RawMessage, error) { return nil, d.err }
func (d *probeFixture) Diagnostic() map[string]any {
	operation := "list_windows"
	if d.closed {
		operation = "end_session"
	}
	return map[string]any{"connected": true, "last_failure": map[string]any{"operation": operation, "kind": "tool_error"}}
}
func (d *probeFixture) Close() error { d.closed = true; return nil }

func TestProbeKeepsReadFailureBeforeClosingSession(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{
		{errors.New("app_discovery_failed"), "app_discovery_failed"},
		{errors.New("window_discovery_failed"), "window_discovery_failed"},
		{context.DeadlineExceeded, "driver_apps_probe_timeout"},
		{errors.New("private tool response"), "driver_apps_probe_failed"},
	} {
		d := &probeFixture{err: tc.err}
		status, err := probeDriver(context.Background(), d)
		if err == nil || err.Error() != tc.code || !d.closed {
			t.Fatalf("probe: %v closed=%v", err, d.closed)
		}
		encoded, _ := json.Marshal(status)
		if status["connected"] != false || status["last_failure"].(map[string]any)["operation"] != "list_windows" {
			t.Fatalf("failure lost during cleanup: %s", encoded)
		}
		if safeDriverError(err) != tc.code {
			t.Fatal("safe classification lost")
		}
	}
	d := &probeFixture{}
	if status, err := probeDriver(context.Background(), d); status != nil || err != nil || d.closed {
		t.Fatal("healthy driver rejected")
	}
}
