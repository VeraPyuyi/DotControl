package main

import "testing"

func TestPermissionsRequireDaemonEvidence(t *testing.T) {
	for _, data := range []string{
		`{`, `{}`, `{"daemon_running":true,"status":"unknown"}`,
		`{"source":{"attribution":"caller"},"accessibility":true,"screen_recording":true}`,
		`{"source":{"attribution":"driver-daemon"},"accessibility":true}`,
		`{"source":{"attribution":"driver-daemon"},"accessibility":false,"screen_recording":true}`,
		`{"source":{"attribution":"driver-daemon"},"accessibility":true,"screen_recording":false}`,
	} {
		if got := parsePermissions([]byte(data)); got.ready() {
			t.Fatalf("accepted unverified grants: %s", data)
		}
	}
	got := parsePermissions([]byte(`{"source":{"attribution":"driver-daemon"},"accessibility":true,"screen_recording":true,"screen_recording_capturable":null,"direct_capture_status":"not_checked"}`))
	if !got.ready() || got.State != "granted" {
		t.Fatalf("daemon grants not recognized: %+v", got)
	}
	if (permissionStatus{}).ready() {
		t.Fatal("empty state considered ready")
	}
}
