package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/dotcontrol/dotcontrol/internal/config"
	"github.com/dotcontrol/dotcontrol/internal/dotbridge"
	"github.com/dotcontrol/dotcontrol/internal/localapi"
	"github.com/dotcontrol/dotcontrol/internal/vault"
)

func bridgeCLI(ctx context.Context, dir string, c config.Config, v vault.Vault, args []string) error {
	if len(args) == 0 {
		return errors.New("bridge command required")
	}
	switch args[0] {
	case "enable":
		return bridgePost(ctx, c, v, "/admin/bridge/enable", nil)
	case "disable":
		return bridgePost(ctx, c, v, "/admin/bridge/disable", nil)
	case "status":
		return bridgePost(ctx, c, v, "/admin/bridge/status", nil)
	case "pair":
		return bridgePost(ctx, c, v, "/admin/bridge/pair", map[string]any{})
	case "revoke-client":
		if len(args) < 2 {
			return errors.New("client_id required")
		}
		return bridgePost(ctx, c, v, "/admin/bridge/revoke-client", map[string]string{"client_id": args[1]})
	case "probe":
		if len(args) < 2 {
			return errors.New("client_id required")
		}
		return bridgePost(ctx, c, v, "/admin/bridge/probe", map[string]string{"client_id": args[1]})
	case "jobs":
		if len(args) < 2 {
			return errors.New("client_id required")
		}
		return bridgeClientGet(ctx, dir, c.Listen, args[1], "/dot-bridge/v1/status")
	case "get":
		if len(args) < 3 {
			return errors.New("client_id and job_id required")
		}
		return bridgeClientGet(ctx, dir, c.Listen, args[1], "/dot-bridge/v1/jobs/"+args[2])
	case "cancel":
		if len(args) < 3 {
			return errors.New("client_id and job_id required")
		}
		return bridgeClientPost(ctx, dir, c.Listen, args[1], "/dot-bridge/v1/jobs/"+args[2]+"/cancel", nil)
	default:
		return errors.New("unknown_bridge_command")
	}
}

func bridgePost(ctx context.Context, c config.Config, v vault.Vault, path string, body any) error {
	token, err := v.Get("admin-token")
	if err != nil {
		return errors.New("local_credential_missing")
	}
	data, err := localapi.Client{Address: c.Listen, Token: token}.Post(ctx, path, body)
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func bridgeClientGet(ctx context.Context, dir, listen, clientID, path string) error {
	token, err := loadPairingToken(dir, clientID)
	if err != nil {
		return err
	}
	return bridgeClientRequest(ctx, listen, token, http.MethodGet, path, nil)
}

func bridgeClientPost(ctx context.Context, dir, listen, clientID, path string, body any) error {
	token, err := loadPairingToken(dir, clientID)
	if err != nil {
		return err
	}
	return bridgeClientRequest(ctx, listen, token, http.MethodPost, path, body)
}

func bridgeClientRequest(ctx context.Context, listen, token, method, path string, body any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+listen+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect_refused") }}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	fmt.Println(string(data))
	if res.StatusCode >= 400 {
		return errors.New("bridge_request_failed")
	}
	return nil
}

func loadPairingToken(dir, clientID string) (string, error) {
	path := filepath.Join(dir, "dot-bridge", "pairing-"+clientID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", errors.New("pairing_file_missing")
	}
	var file dotbridge.PairingFile
	if json.Unmarshal(data, &file) != nil || file.Token == "" {
		return "", errors.New("pairing_file_invalid")
	}
	return file.Token, nil
}

// Optional bridge failures preserve the existing runtime and its default catalog.
func openRuntimeDotBridge(ctx context.Context, dir string, c config.Config, v vault.Store, paused, revoked func() bool) (*dotbridge.Service, error) {
	if e := dotbridge.EnsureStateKey(v); e != nil {
		return nil, e
	}
	return dotbridge.Open(ctx, dotbridge.Config{Root: filepath.Join(dir, "dot-bridge"), Listen: c.Listen, Vault: v, MCPQueue: c.DeviceID, Paused: paused, Revoked: revoked})
}
