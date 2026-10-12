//go:build !windows && !darwin

package desktop

import "errors"

func PrepareInteractiveIO() error { return nil }

func OpenInteractive(string, []string) error { return errors.New("local_terminal_unavailable") }
