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
	if o.native != nil {
		return o.native.Diagnostic()
	}
	if o.driverDiagnostic != nil {
		return o.driverDiagnostic
	}
	return map[string]any{"connected": false}
}
