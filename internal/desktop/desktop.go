// Package desktop integrates only with the signed-in user's desktop session.
package desktop

import (
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func ProgramRoot() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("LOCALAPPDATA"), "DotControl")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Application Support", "DotControl", "program")
}
func OpenURL(value string) error {
	u, e := url.Parse(value)
	if e != nil {
		return errors.New("invalid_url")
	}
	if u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "x-apple.systempreferences" && u.Scheme != "codex" {
		return errors.New("invalid_url")
	}
	return open(value)
}

// StartDetached never launches a console window. No secrets are passed in args.
func StartDetached(path string, args ...string) error {
	cmd := exec.Command(path, args...)
	detach(cmd)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		return errors.New("agent_start_failed")
	}
	return cmd.Process.Release()
}
func QuotedWindows(s string) string { return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"` }
