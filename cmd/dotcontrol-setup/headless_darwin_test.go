//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDarwinHeadlessPayloadAcceptsKnownSystemTemporaryAliases(t *testing.T) {
	for _, base := range []string{"", "/tmp", "/var/tmp"} {
		t.Run(base, func(t *testing.T) {
			temp, e := os.MkdirTemp(base, "dotcontrol-headless-alias-")
			if e != nil {
				t.Fatal(e)
			}
			defer os.RemoveAll(temp)
			physical, e := filepath.EvalSymlinks(temp)
			if e != nil {
				t.Fatal(e)
			}
			dest, e := preparePayloadDestination(filepath.Join(temp, "payload"))
			if e != nil || dest != filepath.Join(physical, "payload") {
				t.Fatalf("native headless payload path=%q err=%v", dest, e)
			}
			custom := filepath.Join(temp, "custom-link")
			if e = os.Symlink(physical, custom); e != nil {
				t.Fatal(e)
			}
			if _, e := preparePayloadDestination(filepath.Join(custom, "payload")); e == nil {
				t.Fatal("arbitrary payload alias accepted")
			}
		})
	}
}
