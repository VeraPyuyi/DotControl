package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/dotcontrol/dotcontrol/internal/config"
	"github.com/dotcontrol/dotcontrol/internal/desktop"
	"github.com/dotcontrol/dotcontrol/internal/localapi"
	"github.com/dotcontrol/dotcontrol/internal/vault"
	"github.com/gofrs/flock"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Upgrades use the drain-only endpoint. Ordinary user quit/uninstall retains
// stopLocal; a rejected upgrade must never fall through to that cancel path.
var errUpgradeExitUncertain = errors.New("agent_exit_recovery_pending")

func stopForUpgrade(ctx context.Context, dir string) error {
	return stopForUpgradeWithEvidence(ctx, dir, nil)
}

func stopForUpgradeWithEvidence(ctx context.Context, dir string, record func([]int) error) (resultErr error) {
	c, e := config.Load(dir)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return errors.New("existing_configuration_invalid")
	}
	lock := flock.New(filepath.Join(dir, "engine.lock"))
	defer lock.Close()
	ok, e := lock.TryLock()
	if e != nil {
		return errors.New("existing_instance_locked")
	}
	if ok {
		return nil
	}
	token, e := vault.New(dir).Get("admin-token")
	if e != nil {
		return errors.New("local_credential_missing")
	}
	client := localapi.Client{Address: c.Listen, Token: token}
	check, cancel := context.WithTimeout(ctx, 3*time.Second)
	data, e := client.Post(check, "/admin/status", map[string]any{})
	cancel()
	var status struct {
		Device struct {
			ID string `json:"device_id"`
		} `json:"device"`
		Runtime struct {
			PID        int    `json:"pid"`
			Executable string `json:"executable"`
		} `json:"runtime"`
	}
	if e != nil || json.Unmarshal(data, &status) != nil || status.Device.ID != c.DeviceID {
		return errors.New("agent_shutdown_identity_unverified")
	}
	var tree *desktop.ProcessTree
	if status.Runtime.PID > 0 && status.Runtime.Executable != "" {
		tree, e = desktop.ObserveProcessTree(ctx, status.Runtime.PID)
		if e != nil {
			return e
		}
		if record != nil {
			if e = record(tree.PIDs()); e != nil {
				return e
			}
		}
		watch, cancelWatch := context.WithCancel(ctx)
		watchDone := make(chan error, 1)
		go func() { watchDone <- tree.Watch(watch, record) }()
		defer func() {
			cancelWatch()
			watchErr := <-watchDone
			if watchErr != nil {
				resultErr = errors.Join(errUpgradeExitUncertain, resultErr, watchErr)
			}
			if record != nil {
				if e := record(tree.PIDs()); e != nil {
					resultErr = errors.Join(errUpgradeExitUncertain, resultErr, e)
				}
			}
		}()
	}
	stop, done := context.WithTimeout(ctx, 45*time.Second)
	_, e = client.Post(stop, "/admin/upgrade/shutdown", map[string]any{})
	done()
	if e != nil {
		var response *localapi.ResponseError
		if errors.As(e, &response) {
			if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusMethodNotAllowed || response.StatusCode == http.StatusNotImplemented {
				return errors.New("legacy_agent_stop_required")
			}
			return errors.New("agent_shutdown_refused")
		}
	}
	if tree == nil {
		return errors.Join(errUpgradeExitUncertain, errors.New("agent_process_identity_unverified"))
	}
	wait, done := context.WithTimeout(ctx, 30*time.Second)
	defer done()
	if e = tree.Wait(wait); e != nil {
		return errors.Join(errUpgradeExitUncertain, e)
	}
	ok, e = lock.TryLock()
	if e != nil || !ok {
		return errors.Join(errUpgradeExitUncertain, errors.New("agent_stop_timeout"))
	}
	return nil
}
