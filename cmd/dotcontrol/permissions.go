package main

import "encoding/json"

// Permission grants and an actual returned screenshot are separate evidence.
// Only the daemon can report the identity to which macOS grants apply.
type permissionStatus struct {
	State           string `json:"state"`
	Accessibility   *bool  `json:"accessibility,omitempty"`
	ScreenRecording *bool  `json:"screen_recording,omitempty"`
}

func (p permissionStatus) ready() bool { return p.State == "granted" || p.State == "not_required" }

func parsePermissions(data []byte) permissionStatus {
	var reply struct {
		Status          string `json:"status"`
		Accessibility   *bool  `json:"accessibility"`
		ScreenRecording *bool  `json:"screen_recording"`
		Source          struct {
			Attribution string `json:"attribution"`
		} `json:"source"`
	}
	if json.Unmarshal(data, &reply) != nil || reply.Status == "unknown" || reply.Source.Attribution != "driver-daemon" || reply.Accessibility == nil || reply.ScreenRecording == nil {
		return permissionStatus{State: "unknown"}
	}
	p := permissionStatus{State: "required", Accessibility: reply.Accessibility, ScreenRecording: reply.ScreenRecording}
	if *reply.Accessibility && *reply.ScreenRecording {
		p.State = "granted"
	}
	return p
}
