//go:build windows

package connections

import (
	"golang.org/x/sys/windows"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsStateUsesProtectedCurrentUserACL(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "connections")
	if err := NewStore(dir).SetCodexExecutable(""); err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{dir, filepath.Join(dir, "connections.json"), filepath.Join(dir, "connections.lock")} {
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
		// Check the sole ACE principal through the descriptor's normalized SDDL.
		if !strings.Contains(sd.String(), user.User.Sid.String()) {
			t.Fatal("ACL principal differs from current user")
		}
	}
}
