package main

import (
	"context"
	"errors"
)

// Legacy command intentionally performs no parsing, state access, or SSH.
func experimentCLI(context.Context, string, []string) error { return errors.New("feature_retired") }
