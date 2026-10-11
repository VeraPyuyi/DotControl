//go:build !darwin

package main

import "context"

func installMacComponents(context.Context, string) error { return nil }
