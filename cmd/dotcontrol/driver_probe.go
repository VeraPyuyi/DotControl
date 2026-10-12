package main

import (
	"context"
	"encoding/json"
	"errors"
)

type appProbe interface {
	Apps(context.Context) (json.RawMessage, error)
	Diagnostic() map[string]any
	Close() error
}

// Keep the driver's sanitized failure before closing its session. Cleanup can
// produce a different error, and the unusable driver is not kept by the owner.
func probeDriver(ctx context.Context, d appProbe) (map[string]any, error) {
	if origin, ok := d.(interface{ SetDiagnosticOrigin(string) }); ok {
		origin.SetDiagnosticOrigin("startup_probe")
		defer origin.SetDiagnosticOrigin("desktop_request")
	}
	_, err := d.Apps(ctx)
	if err == nil {
		return nil, nil
	}
	status := d.Diagnostic()
	status["connected"] = false
	_ = d.Close()
	if errors.Is(err, context.DeadlineExceeded) {
		return status, errors.New("driver_apps_probe_timeout")
	}
	switch err.Error() {
	case "app_discovery_failed", "window_discovery_failed", "driver_transport_failed", "driver_empty_response":
		return status, errors.New(err.Error())
	default:
		return status, errors.New("driver_apps_probe_failed")
	}
}

// Caller holds o.mu. Failed startup diagnostics remain visible even though the
// failed connection has been closed and is unavailable to desktop operations.
func (o *runtimeOwner) driverStatus() map[string]any {
	status := map[string]any{"connected": false}
	if o.native != nil {
		status = o.native.Diagnostic()
	} else if o.driverDiagnostic != nil {
		for k, v := range o.driverDiagnostic {
			status[k] = v
		}
	}
	if failure := status["last_failure"]; failure != nil {
		o.lastDriverFailure = failure
	} else if o.lastDriverFailure != nil {
		status["last_failure"] = o.lastDriverFailure
	}
	return status
}
