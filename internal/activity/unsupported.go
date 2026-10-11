//go:build !windows && !darwin

package activity

import (
	"context"
	"errors"
)

func Start(context.Context, string) (*Monitor, error) {
	return &Monitor{}, errors.New("desktop execution supports Windows and macOS only")
}
