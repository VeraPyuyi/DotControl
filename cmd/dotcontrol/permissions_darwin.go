package main

import (
	"context"
	"github.com/dotcontrol/dotcontrol/internal/driver"
	"os/exec"
	"time"
)

func checkPermissions(ctx context.Context, path string) permissionStatus {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if driver.ValidateExecutable(ctx, path) != nil {
		return permissionStatus{State: "signature_check_failed"}
	}
	data, err := exec.CommandContext(ctx, path, "permissions", "status", "--json").Output()
	if err != nil {
		return permissionStatus{State: "unknown"}
	}
	return parsePermissions(data)
}
