//go:build !windows && !darwin

package desktop

import (
	"context"
	"errors"
)

func SnapshotStartup() (StartupSnapshot, error) {
	return StartupSnapshot{}, errors.New("unsupported_os")
}
func RestoreStartup(StartupSnapshot) error { return errors.New("unsupported_os") }
func listProgramProcesses(context.Context) ([]programProcess, error) {
	return nil, errors.New("unsupported_os")
}
func startupReferenceText(context.Context) (string, error) { return "", errors.New("unsupported_os") }
