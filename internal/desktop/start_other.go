//go:build !darwin

package desktop

func ValidateAutostart(state string) error { return nil }

func StartAgent(executable, state string) error {
	if err := Autostart(executable, state, true); err != nil {
		return err
	}
	return StartDetached(executable, "--state", state, "serve")
}
func EnsureAgent(executable, state string) error {
	return StartDetached(executable, "--state", state, "serve")
}
func AutostartStatus(executable, state string) StartupStatus {
	enabled := AutostartEnabled()
	reason := "not_configured"
	if enabled {
		reason = "registered"
	}
	return StartupStatus{Configured: enabled, Registered: enabled, ProgramMatches: enabled, Reason: reason}
}
