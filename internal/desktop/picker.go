package desktop

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ChoosePath is invoked only from an authenticated local UI click. Scripts are
// fixed literals: no selected path, secret, or caller text is interpolated.
func ChoosePath(ctx context.Context, kind string) (string, error) {
	if kind != "directory" && kind != "identity" && kind != "executable" {
		return "", errors.New("invalid_picker_kind")
	}
	bounded, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		script := `[Console]::OutputEncoding=[System.Text.UTF8Encoding]::new($false); Add-Type -AssemblyName System.Windows.Forms; $picker=[System.Windows.Forms.OpenFileDialog]::new(); $picker.CheckFileExists=$true; if($picker.ShowDialog() -eq 'OK'){[Console]::Write($picker.FileName)}`
		if kind == "directory" {
			script = `[Console]::OutputEncoding=[System.Text.UTF8Encoding]::new($false); Add-Type -AssemblyName System.Windows.Forms; $picker=[System.Windows.Forms.FolderBrowserDialog]::new(); if($picker.ShowDialog() -eq 'OK'){[Console]::Write($picker.SelectedPath)}`
		}
		cmd = exec.CommandContext(bounded, filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe"), "-NoProfile", "-STA", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", script)
		HideConsole(cmd)
	case "darwin":
		script := `POSIX path of (choose file with prompt "Choose a local file; its contents will not be read")`
		if kind == "directory" {
			script = `POSIX path of (choose folder with prompt "Choose a project folder for DotControl tasks")`
		}
		cmd = exec.CommandContext(bounded, "/usr/bin/osascript", "-e", script)
	default:
		return "", errors.New("file_picker_unavailable")
	}
	data, err := cmd.Output()
	if err != nil {
		return "", errors.New("file_picker_cancelled_or_unavailable")
	}
	selected := strings.TrimSpace(string(data))
	if selected == "" {
		return "", errors.New("file_picker_cancelled")
	}
	if !filepath.IsAbs(selected) {
		return "", errors.New("invalid_selected_path")
	}
	return filepath.Clean(selected), nil
}
