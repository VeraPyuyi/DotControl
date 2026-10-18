//go:build darwin

package desktop

import (
	"os"
	"os/exec"
	"path/filepath"

	"syscall"
)

func detach(cmd *exec.Cmd)      { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
func open(value string) error   { return StartDetached("/usr/bin/open", value) }
func HideConsole(cmd *exec.Cmd) {}
func plistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", "org.dotcontrol.agent.plist")
}
