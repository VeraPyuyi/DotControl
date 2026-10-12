//go:build darwin

package desktop

import (
	"bytes"
	"encoding/xml"
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
func xmlText(value string) string {
	var out bytes.Buffer
	_ = xml.EscapeText(&out, []byte(value))
	return out.String()
}
func Autostart(executable, state string, enable bool) error {
	if enable {
		return StartAgent(executable, state)
	}
	return localAgent().remove(state)
}
func AutostartEnabled() bool { return AutostartStatus("", "").Enabled() }
