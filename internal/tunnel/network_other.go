//go:build !windows && !darwin

package tunnel

import "context"

func readSystemProxy(context.Context) (systemProxy, error) { return systemProxy{}, nil }
