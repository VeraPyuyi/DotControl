//go:build windows

package desktop

import (
	"errors"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"
)

func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x00000200 | 0x00000008}
}
func open(value string) error {
	cmd := exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", value)
	detach(cmd)
	if childrenJob != 0 {
		cmd.SysProcAttr.CreationFlags |= 0x01000000
	}
	if cmd.Start() != nil {
		return errors.New("browser_open_failed")
	}
	return cmd.Process.Release()
}
func HideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}

// Keep the job handle open until process exit. Windows then terminates owned
// child transports even after a crash, so an orphan cannot retain a tunnel.
var childrenJob windows.Handle

func OwnChildren() error {
	h, e := windows.CreateJobObject(nil, nil)
	if e != nil {
		return errors.New("process_supervision_unavailable")
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK
	if _, e = windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); e != nil {
		windows.CloseHandle(h)
		return errors.New("process_supervision_unavailable")
	}
	if e = windows.AssignProcessToJobObject(h, windows.CurrentProcess()); e != nil {
		windows.CloseHandle(h)
		return errors.New("process_supervision_unavailable")
	}
	childrenJob = h
	return nil
}
func Autostart(executable, state string, enable bool) error {
	key, e := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE|registry.QUERY_VALUE)
	if e != nil {
		return errors.New("autostart_unavailable")
	}
	defer key.Close()
	if !enable {
		current, _, readErr := key.GetStringValue("DotControl")
		if readErr == registry.ErrNotExist {
			return nil
		}
		if readErr != nil {
			return errors.New("autostart_unavailable")
		}
		if !strings.Contains(current, " --state "+QuotedWindows(state)+" serve") {
			return nil
		}
		e = key.DeleteValue("DotControl")
		if e == registry.ErrNotExist {
			return nil
		}
		return e
	}
	return key.SetStringValue("DotControl", QuotedWindows(executable)+" --state "+QuotedWindows(state)+" serve")
}
func AutostartEnabled() bool {
	key, e := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.QUERY_VALUE)
	if e != nil {
		return false
	}
	defer key.Close()
	v, _, e := key.GetStringValue("DotControl")
	return e == nil && v != ""
}
