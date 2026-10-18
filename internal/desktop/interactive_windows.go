//go:build windows

package desktop

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// exec.Cmd's nil streams use NUL even with CREATE_NEW_CONSOLE. Rebind only to
// the local console device before the SSH CLI tests and inherits its handles.
func PrepareInteractiveIO() error {
	in, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		// The installed agent has the Windows GUI subsystem. CREATE_NEW_CONSOLE
		// alone does not allocate console handles for that build; allocate one
		// explicitly only in this user-requested interactive CLI entry point.
		allocated, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("AllocConsole").Call()
		if allocated == 0 {
			return errors.New("local_interactive_terminal_required")
		}
		in, err = os.OpenFile("CONIN$", os.O_RDWR, 0)
		if err != nil {
			return errors.New("local_interactive_terminal_required")
		}
	}
	out, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
	if err != nil {
		in.Close()
		return errors.New("local_interactive_terminal_required")
	}
	os.Stdin = in
	os.Stdout = out
	os.Stderr = out
	return nil
}

// A visible console is intentional: only OpenSSH in this user-owned window
// reads the password. Do not attach a pipe or redirect its output to a log.
func OpenInteractive(executable string, args []string) error {
	if !filepath.IsAbs(executable) {
		return errors.New("invalid_local_executable")
	}
	cmd := exec.Command(executable, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000010}
	if err := cmd.Start(); err != nil {
		return errors.New("local_terminal_failed")
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
