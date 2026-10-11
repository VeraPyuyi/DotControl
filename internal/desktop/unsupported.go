//go:build !windows && !darwin

package desktop

import (
	"errors"
	"os/exec"
)

func detach(*exec.Cmd)                     {}
func open(string) error                    { return errors.New("unsupported_os") }
func HideConsole(*exec.Cmd)                {}
func OwnChildren() error                   { return nil }
func Autostart(string, string, bool) error { return errors.New("unsupported_os") }
func AutostartEnabled() bool               { return false }
