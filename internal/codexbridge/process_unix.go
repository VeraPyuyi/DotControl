//go:build !windows

package codexbridge

import "os/exec"

func hideProcess(*exec.Cmd) {}
