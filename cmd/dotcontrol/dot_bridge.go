package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/config"
	"github.com/dotcontrol/dotcontrol/internal/dotbridge"
	"github.com/dotcontrol/dotcontrol/internal/localapi"
	"github.com/dotcontrol/dotcontrol/internal/vault"
)

func bridgeCLI(ctx context.Context, dir string, c config.Config, v vault.Store, args []string) error {
	if len(args) == 0 {
		return errors.New("bridge command required")
	}
	switch args[0] {
	case "enable":
		if len(args) != 1 {
			return errors.New("usage: bridge enable")
		}
		if !c.DotBridgeEnabled {
			// Reload installed configuration instead of persisting a stale CLI snapshot.
			current, err := config.Load(dir)
			if err != nil {
				return err
			}
			current.DotBridgeEnabled = true
			if err := config.Save(dir, current); err != nil {
				return err
			}
			fmt.Println(`{"configured":true,"restart_required":true,"next":"Restart the existing runtime, then run dotcontrol bridge enable"}`)
			return nil
		}
		return bridgePost(ctx, c, v, "/admin/bridge/enable", nil)
	case "disable":
		return bridgePost(ctx, c, v, "/admin/bridge/disable", nil)
	case "status":
		return bridgePost(ctx, c, v, "/admin/bridge/status", nil)
	case "pair":
		return bridgePair(ctx, c, v)
	case "revoke-client":
		if len(args) < 2 {
			return errors.New("client_id required")
		}
		return bridgePost(ctx, c, v, "/admin/bridge/revoke-client", map[string]string{"client_id": args[1]})
	case "probe":
		if len(args) < 2 {
			return errors.New("client_id required")
		}
		mode := "answer"
		if len(args) != 2 {
			if len(args) != 4 || args[2] != "--mode" || (args[3] != "answer" && args[3] != "task") {
				return errors.New("usage: bridge probe <client_id> [--mode answer|task]")
			}
			mode = args[3]
		}
		return bridgePost(ctx, c, v, "/admin/bridge/probe", map[string]string{"client_id": args[1], "mode": mode})
	case "jobs":
		if len(args) < 2 {
			return errors.New("client_id required")
		}
		return bridgeClientGet(ctx, dir, c.Listen, args[1], "/dot-bridge/v1/jobs")
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

func bridgePair(ctx context.Context, c config.Config, v vault.Store) error {
	data, err := bridgePostRaw(ctx, c, v, "/admin/bridge/pair", map[string]any{})
	if err != nil {
		return err
	}
	var full map[string]json.RawMessage
	if json.Unmarshal(data, &full) != nil {
		return errors.New("invalid_agent_response")
	}
	out := make(map[string]string, 3)
	for _, key := range []string{"pairing_file", "client_id", "queue_id"} {
		if raw, ok := full[key]; ok {
			var value string
			if json.Unmarshal(raw, &value) == nil && value != "" {
				out[key] = value
			}
		}
	}
	if out["pairing_file"] == "" {
		return errors.New("invalid_agent_response")
	}
	b, err := json.Marshal(out)
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}

func bridgePost(ctx context.Context, c config.Config, v vault.Store, path string, body any) error {
	data, err := bridgePostRaw(ctx, c, v, path, body)
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func bridgePostRaw(ctx context.Context, c config.Config, v vault.Store, path string, body any) (json.RawMessage, error) {
	if err := validateBridgeListen(c.Listen); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	token, err := v.Get("admin-token")
	if err != nil {
		return nil, errors.New("local_credential_missing")
	}
	return localapi.Client{Address: c.Listen, Token: token}.Post(ctx, path, body)
}

func bridgeClientGet(ctx context.Context, dir, listen, clientID, path string) error {
	if err := validateBridgeListen(listen); err != nil {
		return err
	}
	token, err := loadPairingToken(dir, clientID)
	if err != nil {
		return err
	}
	return bridgeClientRequest(ctx, listen, token, http.MethodGet, path, nil)
}

func bridgeClientPost(ctx context.Context, dir, listen, clientID, path string, body any) error {
	if err := validateBridgeListen(listen); err != nil {
		return err
	}
	token, err := loadPairingToken(dir, clientID)
	if err != nil {
		return err
	}
	return bridgeClientRequest(ctx, listen, token, http.MethodPost, path, body)
}

func bridgeClientRequest(ctx context.Context, listen, token, method, path string, body any) error {
	if err := validateBridgeListen(listen); err != nil {
		return err
	}
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
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect_refused") }}
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
	file, err := dotbridge.ReadPairingFile(filepath.Join(dir, "dot-bridge"), clientID)
	if err != nil {
		return "", err
	}
	return file.Token, nil
}

func validateBridgeListen(listen string) error {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return errors.New("invalid_local_address")
	}
	ip, err := netip.ParseAddr(host)
	n, portErr := strconv.Atoi(port)
	if err != nil || !ip.IsLoopback() || ip.Zone() != "" || portErr != nil || n < 1 || n > 65535 {
		return errors.New("invalid_local_address")
	}
	return nil
}

// Optional bridge failures preserve the existing runtime and its default catalog.
func openRuntimeDotBridge(ctx context.Context, dir string, c config.Config, v vault.Store, paused, revoked func() bool) (*dotbridge.Service, error) {
	if e := dotbridge.EnsureStateKey(v); e != nil {
		return nil, e
	}
	return dotbridge.Open(ctx, dotbridge.Config{Root: filepath.Join(dir, "dot-bridge"), Listen: c.Listen, Vault: v, MCPQueue: c.DeviceID, Paused: paused, Revoked: revoked})
}
