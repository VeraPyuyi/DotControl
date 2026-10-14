//go:build darwin

package desktop

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
)

func PrepareInteractiveIO() error { return nil }

func OpenInteractive(executable string, args []string) error {
	if !filepath.IsAbs(executable) {
		return errors.New("invalid_local_executable")
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'" }
	command := quote(executable)
	for _, a := range args {
		command += " " + quote(a)
	}
	// Escape as an AppleScript string, then let Terminal own the interactive TTY.
	literal := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\r", `\r`, "\n", `\n`).Replace(command)
	if err := exec.Command("/usr/bin/osascript", "-e", `tell application "Terminal" to do script "`+literal+`"`).Run(); err != nil {
		return errors.New("local_terminal_failed")
	}
	return nil
}
