package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

type Config struct {
	Network            Network `json:"network"`
	HistoryEnabled     bool    `json:"history_enabled,omitempty"`
	DeviceID           string  `json:"device_id"`
	Name               string  `json:"name"`
	Listen             string  `json:"listen"`
	DriverPath         string  `json:"driver_path"`
	ActivityHelper     string  `json:"activity_helper,omitempty"`
	IdleSeconds        int     `json:"idle_seconds"`
	TunnelPath         string  `json:"tunnel_path,omitempty"`
	TunnelID           string  `json:"tunnel_id,omitempty"`
	GatewayListen      string  `json:"gateway_listen,omitempty"`
	TerminalOnly       bool    `json:"terminal_only,omitempty"`
	TerminalEnabled    bool    `json:"terminal_enabled,omitempty"`
	TerminalProfileID  string  `json:"terminal_profile_id,omitempty"`
	TerminalServerID   string  `json:"terminal_server_id,omitempty"`
	TerminalConfigHash string  `json:"terminal_config_hash,omitempty"`
}

// Network affects only the official tunnel control plane. ProxyURL is never a
// credential store; authenticated environment values remain process-local.
type Network struct {
	Mode     string `json:"mode"`
	ProxyURL string `json:"proxy_url,omitempty"`
}

func ValidateNetwork(n Network) error {
	if n.Mode != "" && n.Mode != "auto" && n.Mode != "direct" && n.Mode != "manual" {
		return errors.New("invalid_network_mode")
	}
	if n.ProxyURL != "" {
		u, err := url.Parse(n.ProxyURL)
		if err != nil {
			return errors.New("invalid_proxy_url")
		}
		if u.User != nil {
			return errors.New("requires_local_proxy_auth")
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return errors.New("proxy_scheme_unsupported")
		}
		if u.Hostname() == "" || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || strings.ContainsAny(n.ProxyURL, "\r\n\t ") {
			return errors.New("invalid_proxy_url")
		}
	}
	if n.Mode == "manual" && n.ProxyURL == "" {
		return errors.New("manual_proxy_required")
	}
	if n.Mode != "manual" && n.ProxyURL != "" {
		return errors.New("proxy_requires_manual_mode")
	}
	return nil
}

func DefaultDir() string {
	d, e := os.UserConfigDir()
	if e != nil {
		d = "."
	}
	return filepath.Join(d, "DotControl")
}
func Random() string {
	var b [32]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b[:])
}
func New(name, driver string) Config {
	if name == "" {
		name = "DotControl " + runtime.GOOS
	}
	return Config{Network: Network{Mode: "auto"}, DeviceID: "dev_" + Random()[:24], Name: name, Listen: "127.0.0.1:9317", DriverPath: driver, IdleSeconds: 60}
}

var terminalHash = regexp.MustCompile(`^[a-f0-9]{64}$`)

func validTerminal(c Config) bool {
	return !c.TerminalEnabled || (c.TerminalOnly && c.TerminalProfileID != "" && terminalHash.MatchString(c.TerminalConfigHash))
}

func Load(dir string) (Config, error) {
	var c Config
	b, e := os.ReadFile(filepath.Join(dir, "config.json"))
	if e != nil {
		return c, e
	}
	e = json.Unmarshal(b, &c)
	if e != nil {
		return c, errors.New("invalid_config")
	}
	if c.DeviceID == "" || c.IdleSeconds < 1 || c.IdleSeconds > 86400 || !validTerminal(c) {
		return c, errors.New("invalid_config")
	}
	if e = ValidateNetwork(c.Network); e != nil {
		return c, e
	}
	if c.Network.Mode == "" {
		c.Network.Mode = "auto"
	}
	return c, nil
}
func Save(dir string, c Config) error {
	if e := ValidateNetwork(c.Network); e != nil {
		return e
	}
	if c.Network.Mode == "" {
		c.Network.Mode = "auto"
	}
	if !validTerminal(c) {
		return errors.New("invalid_terminal_configuration")
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	b, e := json.MarshalIndent(c, "", "  ")
	if e != nil {
		return e
	}
	tmp := filepath.Join(dir, "config.json.tmp")
	if e = os.WriteFile(tmp, b, 0600); e != nil {
		return e
	}
	return os.Rename(tmp, filepath.Join(dir, "config.json"))
}
