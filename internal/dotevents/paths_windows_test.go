//go:build windows

package dotevents

import (
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
		acl, _, e := sd.DACL()
		if e != nil {
			t.Fatal(e)
		}
		if acl.AceCount != 1 || !strings.Contains(sd.String(), "D:P") || !strings.Contains(sd.String(), user.User.Sid.String()) {
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
		acl, _, e := sd.DACL()
		if e != nil {
			t.Fatal(e)
		}
		user, e := windows.GetCurrentProcessToken().GetTokenUser()
		if e != nil {
			t.Fatal(e)
		}
		text := sd.String()
		if !strings.Contains(text, user.User.Sid.String()) || strings.Contains(text, ";;;WD") {
			t.Errorf("not restricted to current user: %s", text)
		}
		if acl.AceCount != 1 {
			t.Errorf("unexpected ACL entries for %s: %d", p, acl.AceCount)
		}
	}
}
