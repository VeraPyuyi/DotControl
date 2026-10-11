//go:build darwin

package install

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDarwinSystemTemporaryPathsVerifyAndInstall(t *testing.T) {
	for _, base := range []string{"", "/tmp", "/var/tmp"} {
		name := base
		if name == "" {
			name = "os_default"
		}
		t.Run(name, func(t *testing.T) {
			temp, e := os.MkdirTemp(base, "dotcontrol-install-system-temp-")
			if e != nil {
				t.Fatal(e)
			}
			defer os.RemoveAll(temp)
			payload := bundle(t, "0.2.0", "darwin system temp")
			if _, e = Verify(context.Background(), payload); e != nil {
				t.Fatalf("Verify from OS temp path failed: %v", e)
			}
			o := Options{Root: filepath.Join(temp, "program"), StateDir: filepath.Join(temp, "state"), BundleDir: payload}
			r, e := Apply(context.Background(), o)
			if e != nil {
				t.Fatalf("Apply from OS temp path failed: %v", e)
			}
			physical, e := filepath.EvalSymlinks(temp)
			if e != nil {
				t.Fatal(e)
			}
			if !strings.HasPrefix(r.Current.Dir, filepath.Join(physical, "program")+string(filepath.Separator)) {
				t.Fatalf("installed directory was not normalized: %s", r.Current.Dir)
			}
			if c, e := ReadCurrent(o.Root); e != nil || c != r.Current {
				t.Fatalf("aliased read failed: %#v %v", c, e)
			}
			if _, e = preparePaths(filepath.Join(temp, "missing program"), filepath.Join(physical, "missing program", "state")); e == nil {
				t.Fatal("system spelling bypassed state containment")
			}
		})
	}
}

func TestDarwinCustomSymlinksRemainForbidden(t *testing.T) {
	temp, e := os.MkdirTemp("", "dotcontrol-install-custom-link-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(temp)
	physical := filepath.Join(temp, "physical")
	if e = os.Mkdir(physical, 0700); e != nil {
		t.Fatal(e)
	}
	alias := filepath.Join(temp, "custom-alias")
	if e = os.Symlink(physical, alias); e != nil {
		t.Fatal(e)
	}
	if _, e = preparePaths(filepath.Join(alias, "program"), filepath.Join(temp, "state")); e == nil {
		t.Fatal("custom root symlink accepted")
	}
	if _, e = preparePaths(filepath.Join(temp, "program"), filepath.Join(alias, "state")); e == nil {
		t.Fatal("custom state symlink accepted")
	}
	payload := bundle(t, "0.2.0", "payload")
	bundleAlias := filepath.Join(temp, "bundle-alias")
	if e = os.Symlink(payload, bundleAlias); e != nil {
		t.Fatal(e)
	}
	if _, e = Verify(context.Background(), bundleAlias); e == nil {
		t.Fatal("custom bundle symlink accepted")
	}
}
