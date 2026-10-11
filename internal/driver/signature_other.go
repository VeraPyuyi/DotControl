//go:build !darwin

package driver

import "context"

func ValidateExecutable(context.Context, string) error { return nil }
