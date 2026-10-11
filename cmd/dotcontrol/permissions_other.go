//go:build !darwin

package main

import "context"

func checkPermissions(context.Context, string) permissionStatus {
	return permissionStatus{State: "not_required"}
}
