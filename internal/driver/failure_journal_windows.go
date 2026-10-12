//go:build windows

package driver

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Create with a protected, current-user-only DACL. Existing files are never
// repaired in place: validate the opened handle before writing any bytes.
func openPrivateJournalFile(path string) (*os.File, error) {
	if strings.Contains(strings.TrimPrefix(path, filepath.VolumeName(path)), ":") {
		return nil, errors.New("journal_stream_forbidden")
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	sd, err := windows.SecurityDescriptorFromString("O:" + user.User.Sid.String() + "D:P(A;;FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return nil, err
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	sa := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	h, err := windows.CreateFile(p, windows.GENERIC_WRITE|windows.READ_CONTROL,
		windows.FILE_SHARE_READ, sa, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err == windows.ERROR_FILE_EXISTS || err == windows.ERROR_ALREADY_EXISTS {
		h, err = windows.CreateFile(p, windows.GENERIC_WRITE|windows.READ_CONTROL,
			windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	}
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(h), "native_failure_journal")
	if err := validatePrivateJournalFile(file); err != nil {
		file.Close()
		return nil, err
	}
	// FILE_SHARE_READ excludes a competing writer while this handle is open.
	if _, err := file.Seek(0, 2); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

func validatePrivateJournalFile(file *os.File) error {
	unsafeFile := errors.New("unsafe_journal_file")
	h := windows.Handle(file.Fd())
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(h, &info) != nil || info.NumberOfLinks != 1 || info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		return unsafeFile
	}
	sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return unsafeFile
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return unsafeFile
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return unsafeFile
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || !owner.Equals(user.User.Sid) {
		return unsafeFile
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil || acl.AceCount != 1 {
		return unsafeFile
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if windows.GetAce(acl, 0, &ace) != nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags != 0 {
		return unsafeFile
	}
	sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	const fileAllAccess = 0x001f01ff // WinNT FILE_ALL_ACCESS (SDDL FA).
	if !sid.Equals(user.User.Sid) || ace.Mask&fileAllAccess != fileAllAccess {
		return unsafeFile
	}
	return nil
}
