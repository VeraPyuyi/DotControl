//go:build windows

package dotqueue

import (
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

func privatePath(path string, directory bool) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	inherit := ""
	if directory {
		inherit = "OICI"
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;" + inherit + ";FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return err
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}

func rejectLink(info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return errf("unknown")
	}
	if attr, ok := info.Sys().(*syscall.Win32FileAttributeData); ok && attr.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errf("unknown")
	}
	return nil
}

func rejectHardlink(f *os.File, _ os.FileInfo) error {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); err != nil {
		return err
	}
	if info.NumberOfLinks != 1 {
		return errf("unknown")
	}
	return nil
}

func replaceFile(from, to string) error {
	source, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	target, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	// Windows scanners/readers can temporarily deny replacement even after our
	// own handles close. Retry only these transient errors, under the queue lock.
	deadline := time.Now().Add(time.Second)
	for {
		e := windows.MoveFileEx(source, target, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
		if e == nil || e != windows.ERROR_ACCESS_DENIED && e != windows.ERROR_SHARING_VIOLATION || time.Now().After(deadline) {
			return e
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func syncDirectory(string) error { return nil }

func tryHandleLock(f *os.File) (bool, error) {
	var o windows.Overlapped
	e := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &o)
	if e == windows.ERROR_LOCK_VIOLATION {
		return false, nil
	}
	return e == nil, e
}
func unlockHandle(f *os.File) {
	var o windows.Overlapped
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &o)
}
