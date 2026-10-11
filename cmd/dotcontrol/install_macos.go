//go:build darwin

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func verifyCua(ctx context.Context, path string) error {
	if exec.CommandContext(ctx, "/usr/bin/codesign", "--verify", "--deep", "--strict", "-R", `=anchor apple generic and certificate leaf[subject.OU] = "YCK386LBJ7"`, path).Run() != nil {
		return errors.New("signature_invalid")
	}
	return nil
}
func installMacComponents(ctx context.Context, bundle string) error {
	archive := filepath.Join(bundle, "cua-driver.tar.gz")
	data, e := os.ReadFile(archive)
	if e != nil {
		return errors.New("cua_archive_missing")
	}
	want := "83ffed8d41ef0f133976d594b02eb86dd59ef4a8721396b6e25078a59c6056d6"
	arch := "arm64"
	if runtime.GOARCH == "amd64" {
		want = "c485781a64b6f75ea46f63d03c5b4f646e0f1dcea2c7547805f235259193a156"
		arch = "x86_64"
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != want {
		return errors.New("cua_archive_hash_mismatch")
	}
	temp, e := os.MkdirTemp("", "DotControl-Cua-")
	if e != nil {
		return errors.New("component_stage_failed")
	}
	defer os.RemoveAll(temp)
	// Only the exact pinned upstream archive is extracted; preserve its signed
	// bundle's internal CodeResources symlink rather than flattening/re-signing.
	if exec.CommandContext(ctx, "/usr/bin/tar", "-xzf", archive, "-C", temp).Run() != nil {
		return errors.New("component_stage_failed")
	}
	source := filepath.Join(temp, "cua-driver-rs-0.34.1-darwin-"+arch, "CuaDriver.app")
	if e := verifyCua(ctx, source); e != nil {
		return e
	}
	target := "/Applications/CuaDriver.app"
	if _, e := os.Stat(target); e == nil {
		if e = verifyCua(ctx, target); e != nil {
			return e
		}
		a, _ := os.ReadFile(filepath.Join(source, "Contents", "MacOS", "cua-driver"))
		b, _ := os.ReadFile(filepath.Join(target, "Contents", "MacOS", "cua-driver"))
		if len(a) == 0 || len(b) == 0 || sha256.Sum256(a) != sha256.Sum256(b) {
			return errors.New("existing_cua_version_differs")
		}
	} else if os.IsNotExist(e) {
		if exec.CommandContext(ctx, "/usr/bin/ditto", source, target).Run() != nil {
			return errors.New("applications_folder_permission_required")
		}
		if e = verifyCua(ctx, target); e != nil {
			return e
		}
	} else {
		return errors.New("applications_folder_permission_required")
	}
	home, _ := os.UserHomeDir()
	activity := filepath.Join(home, "Applications", "DotControlActivity.app")
	if exec.CommandContext(ctx, "/usr/bin/ditto", filepath.Join(bundle, "DotControlActivity.app"), activity).Run() != nil {
		return errors.New("activity_install_failed")
	}
	menu := filepath.Join(home, "Applications", "DotControl.app")
	if exec.CommandContext(ctx, "/usr/bin/ditto", filepath.Join(bundle, "DotControlMenu.app"), menu).Run() != nil {
		return errors.New("menu_install_failed")
	}
	return nil
}
