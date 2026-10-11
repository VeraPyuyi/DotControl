//go:build !windows

package desktop

import "context"

func Tray(context.Context, func(string)) {}
