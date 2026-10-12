package main

import (
	"context"
	"errors"
	"github.com/dotcontrol/dotcontrol/internal/activity"
	"github.com/dotcontrol/dotcontrol/internal/config"
	"github.com/dotcontrol/dotcontrol/internal/model"
)

func newRuntimeMonitor(ctx context.Context, c config.Config) *activity.Managed {
	if c.TerminalOnly {
		return activity.NewInactive(ctx, c.ActivityHelper)
	}
	return activity.NewManaged(ctx, c.ActivityHelper)
}

func resumeReadiness(c config.Config, revoked, connected bool, permissions permissionStatus, state model.Activity) error {
	if revoked {
		return errors.New("access_revoked")
	}
	if c.TerminalOnly {
		if !c.TerminalEnabled {
			return errors.New("terminal_not_enabled")
		}
		return nil
	}
	if !connected || !state.Available || state.Locked {
		return errors.New("local_checks_not_ready")
	}
	if !permissions.ready() {
		return errors.New("permissions_not_ready")
	}
	return nil
}
