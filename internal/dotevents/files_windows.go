//go:build windows

package dotevents

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
	// Directory ACL propagation and other Windows readers can briefly deny
	// delete sharing, even while we hold the store lock. Keep the sealed source
	// and retry only these transient errors, as the queue store does.
	deadline := time.Now().Add(time.Second)
	for {
		err := windows.MoveFileEx(source, target, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
		if err == nil || err != windows.ERROR_ACCESS_DENIED && err != windows.ERROR_SHARING_VIOLATION || time.Now().After(deadline) {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func syncDirectory(string) error { return nil }
