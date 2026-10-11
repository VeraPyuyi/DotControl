package driver

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
)

// Every runtime entry point verifies the unmodified, stable upstream app before
// executing any of its code. Hash pinning is a separate installation check.
func ValidateExecutable(ctx context.Context, path string) error {
	if filepath.Clean(path) != "/Applications/CuaDriver.app/Contents/MacOS/cua-driver" {
		return errors.New("driver_install_location_invalid")
	}
	if exec.CommandContext(ctx, "/usr/bin/codesign", "--verify", "--deep", "--strict", "-R", `anchor apple generic and certificate leaf[subject.OU] = "YCK386LBJ7"`, "/Applications/CuaDriver.app").Run() != nil {
		return errors.New("signature_invalid")
	}
	return nil
}
