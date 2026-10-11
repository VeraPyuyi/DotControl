//go:build !darwin

package desktop

// RunChildGuardian is a macOS-only internal entry point. Other platforms keep
// their existing ownership mechanisms (the Windows kill-on-close Job Object).
func RunChildGuardian([]string) (bool, error) { return false, nil }
