//go:build windows

package dotevents

import (
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"unsafe"
)

// protectedUserACL reports whether sd is a protected DACL with exactly one
// full-control entry for sid. It compares principals as SIDs: Windows renders
// an account SID as a two-letter alias when it matches a well-known RID, so
// text such as "LA" for the built-in administrator (the GitHub Windows runner
// user) describes the same SID this process token holds.
func protectedUserACL(sd *windows.SECURITY_DESCRIPTOR, sid *windows.SID) bool {
	flags, _, e := sd.Control()
	if e != nil || flags&windows.SE_DACL_PROTECTED == 0 {
		return false
	}
	acl, _, e := sd.DACL()
	if e != nil || acl == nil || acl.AceCount != 1 {
		return false
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if e = windows.GetAce(acl, 0, &ace); e != nil || ace == nil {
		return false
	}
	principal := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	ok := principal.IsValid() && principal.Equals(sid) && ace.Header.AceType == windows.ACCESS_ALLOWED_ACE_TYPE && ace.Mask == windows.STANDARD_RIGHTS_REQUIRED|windows.SYNCHRONIZE|0x1ff
	runtime.KeepAlive(sd)
	return ok
}

func TestWindowsStoreBoundary(t *testing.T) {
	for _, p := range []string{`\\server\share\events`, `\\?\C:\events`, `\\.\C:\events`, `C:\events:stream`, `C:\events\NUL`, `C:\events\trailing.`, `C:\events\trailing `} {
		if _, e := Open(p, testKey, nil); e == nil {
			t.Errorf("unsafe path %s", p)
		}
	}
	base := t.TempDir()
	target := t.TempDir()
	link := filepath.Join(base, "junction")
	out, e := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
	if e != nil {
		t.Fatalf("junction fixture: %v %s", e, out)
	}
	defer os.Remove(link)
	if _, e = Open(filepath.Join(link, "events"), testKey, nil); e == nil {
		t.Fatal("junction ancestor accepted")
	}
}
func TestMinorWindowsCreatedAncestors(t *testing.T) {
	base := t.TempDir()
	before, e := windows.GetNamedSecurityInfo(base, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(base, "created", "intermediate", "events")
	if _, e = Open(dir, testKey, nil); e != nil {
		t.Fatal(e)
	}
	after, e := windows.GetNamedSecurityInfo(base, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if e != nil {
		t.Fatal(e)
	}
	if before.String() != after.String() {
		t.Fatal("existing parent ACL changed")
	}
	user, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		t.Fatal(e)
	}
	for p := dir; p != base; p = filepath.Dir(p) {
		sd, e := windows.GetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if e != nil {
			t.Fatal(e)
		}
		if !protectedUserACL(sd, user.User.Sid) {
			t.Errorf("new ancestor lacks protected current-user ACL: %s %s", p, sd.String())
		}
	}
}

func TestWindowsPrivacyEveryTransaction(t *testing.T) {
	m, dir := openTestManager(t)
	paths := []string{dir, filepath.Join(dir, stateFileName), filepath.Join(dir, markerName), filepath.Join(dir, lockName)}
	for _, p := range paths {
		sd, e := windows.SecurityDescriptorFromString("D:P(A;;FA;;;WD)")
		if e != nil {
			t.Fatal(e)
		}
		acl, _, e := sd.DACL()
		if e != nil {
			t.Fatal(e)
		}
		if e = windows.SetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := m.Status("alice", "alice"); e != nil {
		t.Fatal(e)
	}
	for _, p := range paths {
		sd, e := windows.GetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if e != nil {
			t.Fatal(e)
		}
		user, e := windows.GetCurrentProcessToken().GetTokenUser()
		if e != nil {
			t.Fatal(e)
		}
		if !protectedUserACL(sd, user.User.Sid) {
			t.Errorf("not restricted to current user: %s", sd.String())
		}
	}
}
