//go:build windows

package historyaccess

import (
	"path/filepath"
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsStateUsesProtectedCurrentUserACL(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "history")
	if _, err := NewStore(dir).InitializeAll(); err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{dir, filepath.Join(dir, "history_access.json"), filepath.Join(dir, "history_access.lock"), filepath.Join(dir, "history_access.initialized")} {
		sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		flags, _, err := sd.Control()
		if err != nil || flags&windows.SE_DACL_PROTECTED == 0 {
			t.Fatal("DACL is not protected", err)
		}
		acl, _, err := sd.DACL()
		if err != nil || acl == nil || acl.AceCount != 1 {
			t.Fatal("unexpected ACE count", err)
		}
		var ace *windows.ACCESS_ALLOWED_ACE
		if err = windows.GetAce(acl, 0, &ace); err != nil {
			t.Fatal(err)
		}
		// SDDL may abbreviate well-known identities (including the CI runner's
		// local administrator). Compare the binary SID instead of its rendering.
		principal := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !principal.IsValid() || !principal.Equals(user.User.Sid) {
			t.Fatal("ACL principal differs from current user")
		}
		const fileAllAccess = windows.STANDARD_RIGHTS_REQUIRED | windows.SYNCHRONIZE | 0x1ff
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Mask != fileAllAccess {
			t.Fatal("unexpected ACE access rights")
		}
		runtime.KeepAlive(sd)
	}
}
