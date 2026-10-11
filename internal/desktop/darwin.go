//go:build darwin

package desktop

import (
	"bytes"
	"encoding/xml"
	"errors"
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
	p := plistPath()
	if !enable {
		e := os.Remove(p)
		if os.IsNotExist(e) {
			return nil
		}
		return e
	}
	if os.MkdirAll(filepath.Dir(p), 0700) != nil {
		return errors.New("autostart_unavailable")
	}
	data := `<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>Label</key><string>org.dotcontrol.agent</string><key>ProgramArguments</key><array><string>` + xmlText(executable) + `</string><string>--state</string><string>` + xmlText(state) + `</string><string>serve</string></array><key>RunAtLoad</key><true/><key>ProcessType</key><string>Interactive</string><key>LimitLoadToSessionType</key><string>Aqua</string><key>ThrottleInterval</key><integer>15</integer></dict></plist>`
	return os.WriteFile(p, []byte(data), 0600)
}
func AutostartEnabled() bool { _, e := os.Stat(plistPath()); return e == nil }
