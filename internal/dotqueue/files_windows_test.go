//go:build windows

package dotqueue

import (
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"unsafe"
)

func TestWindowsPrivacyRepairedOnRead(t *testing.T) {
	s, dir := openTestStore(t)
	j := mustSubmit(t, s, "acl")
	user, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		t.Fatal(e)
	}
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
	if _, e = s.Get("client.a", j.ID); e != nil {
		t.Fatal(e)
	}
	for _, p := range paths {
		sd, e := windows.GetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if e != nil {
			t.Fatal(e)
		}
		flags, _, e := sd.Control()
		if e != nil || flags&windows.SE_DACL_PROTECTED == 0 {
			t.Fatalf("unprotected ACL %s %v", p, e)
		}
		acl, _, e := sd.DACL()
		if e != nil || acl == nil || acl.AceCount != 1 {
			t.Fatalf("ACL count %s %v", p, e)
		}
		var ace *windows.ACCESS_ALLOWED_ACE
		if e = windows.GetAce(acl, 0, &ace); e != nil {
			t.Fatal(e)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sid.IsValid() || !sid.Equals(user.User.Sid) || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Mask != windows.STANDARD_RIGHTS_REQUIRED|windows.SYNCHRONIZE|0x1ff {
			t.Fatalf("ACL principal/rights %s", p)
		}
		runtime.KeepAlive(sd)
	}
}
func TestWindowsReparseAndLocalPaths(t *testing.T) {
	s, dir := openTestStore(t)
	j := mustSubmit(t, s, "junction")
	marker := filepath.Join(dir, markerName)
	if e := os.Remove(marker); e != nil {
		t.Fatal(e)
	}
	target := t.TempDir()
	cmd := exec.Command("cmd.exe", "/c", "mklink", "/J", marker, target)
	if b, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("junction fixture: %v %s", e, b)
	}
	defer os.Remove(marker)
	if _, e := s.Get("client.a", j.ID); e == nil {
		t.Error("marker reparse accepted")
	}
	if _, e := Open(filepath.Join(marker, "child"), testKey); e == nil {
		t.Error("reparse ancestor accepted")
	}
	for _, p := range []string{`\\server\share\queue`, `\\?\C:\queue`, `C:\queue:stream`, `C:relative`} {
		if _, e := Open(p, testKey); e == nil {
			t.Errorf("nonlocal path accepted %q", p)
		}
	}
}
