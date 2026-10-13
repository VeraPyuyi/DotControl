package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/codexbridge"
	"github.com/dotcontrol/dotcontrol/internal/connections"
	"github.com/dotcontrol/dotcontrol/internal/desktop"
	"github.com/dotcontrol/dotcontrol/internal/model"
	"github.com/dotcontrol/dotcontrol/internal/onboard"
)

func (o *runtimeOwner) Connections(ctx context.Context, r onboard.ConnectionsRequest) (any, error) {
	switch r.Operation {
	case "save_server", "check_ssh", "authorize_prepare", "authorize_start", "authorize_status", "pick_public_key", "pick_identity":
		return nil, errors.New("feature_retired")
	}
	if r.RetiredFieldsPresent || r.Server != nil || r.ServerProfileID != "" || r.PublicKeyPath != "" || r.AuthorizationID != "" {
		return nil, errors.New("feature_retired")
	}
	o.connectionsMu.Lock()
	defer o.connectionsMu.Unlock()
	switch r.Operation {
	case "save_codex", "save_execution":
		if err := o.invalidateTerminalConfiguration(); err != nil {
			return nil, err
		}
	}
	store := connections.NewStore(filepath.Join(o.dir, "connections"))
	switch r.Operation {
	case "connect_basic":
		return o.connectBasicTerminal(ctx, store)
	case "terminal_mode", "check_terminal", "enable_terminal":
		return o.configureTerminal(ctx, r)
	case "pick_project":
		return desktop.ChoosePath(ctx, "directory")
	case "pick_codex":
		return desktop.ChoosePath(ctx, "executable")
	case "permissions":
		if r.Execution == nil || !filepath.IsAbs(r.Execution.ProjectDir) {
			return nil, errors.New("invalid_project_dir")
		}
		if stat, err := os.Stat(r.Execution.ProjectDir); err != nil || !stat.IsDir() {
			return nil, errors.New("project_dir_unavailable")
		}
		cfg, err := store.Load()
		if err != nil {
			return nil, err
		}
		found, err := connections.DiscoverCodex(ctx, cfg.CodexExecutable, connections.DiscoveryOptions{})
		if err != nil {
			return nil, err
		}
		bounded, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		cwd, err := codexbridge.ProbeWorkspace(r.Execution.ProjectDir)
		if err != nil {
			return nil, err
		}
		client, err := codexbridge.Start(bounded, found.Executable, cwd)
		if err != nil {
			return nil, err
		}
		defer client.Close()
		if err = client.Call(bounded, "initialize", map[string]any{"clientInfo": map[string]string{"name": "dotcontrol_local_setup", "version": model.Version}, "capabilities": map[string]bool{"experimentalApi": true}}, nil); err != nil {
			return nil, err
		}
		if err = client.Notify(bounded, "initialized", map[string]any{}); err != nil {
			return nil, err
		}
		return codexbridge.ReadPermissionProfiles(bounded, client, cwd)
	case "list":
		return store.Load()
	case "discover":
		cfg, err := store.Load()
		if err != nil {
			return nil, err
		}
		found, err := connections.DiscoverCodex(ctx, cfg.CodexExecutable, connections.DiscoveryOptions{})
		if err != nil {
			return nil, err
		}
		// Only this authenticated local UI receives the actual selectable path.
		return map[string]any{"status": found, "executable": found.Executable}, nil
	case "save_codex":
		found, err := connections.DiscoverCodex(ctx, r.Executable, connections.DiscoveryOptions{})
		if err != nil {
			return nil, err
		}
		if !found.Available {
			return found, nil
		}
		if err = store.SetCodexExecutable(found.Executable); err != nil {
			return nil, err
		}
		return found, nil
	case "save_execution":
		if r.Execution == nil {
			return nil, errors.New("invalid_execution_profile")
		}
		return store.UpsertExecution(*r.Execution)
	default:
		return nil, errors.New("unknown_connection_operation")
	}
}
