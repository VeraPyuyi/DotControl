package main

import "testing"

func TestUpgradeChecksTargetManifestVersion(t *testing.T) {
	if !validInstalledVersion([]byte("9.9.0\n"), "9.9.0") {
		t.Fatal("cross-version upgrade rejected")
	}
	if validInstalledVersion([]byte("0.2.0-alpha.1\n"), "9.9.0") {
		t.Fatal("old binary accepted as new release")
	}
}
