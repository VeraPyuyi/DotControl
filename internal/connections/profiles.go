// Package connections stores local connection references and performs read-only
// connection checks. It never stores credentials or deploys remote code.
package connections

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/netip"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

const SchemaVersion = 1

type Config struct {
	Version           int                `json:"version"`
	CodexExecutable   string             `json:"codex_executable,omitempty"`
	ExecutionProfiles []ExecutionProfile `json:"execution_profiles,omitempty"`
	ServerProfiles    []ServerProfile    `json:"server_profiles,omitempty"`
}

type ExecutionProfile struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	ProjectDir        string   `json:"project_dir"`
	AllowedApps       []string `json:"allowed_apps,omitempty"`
	Presentation      string   `json:"presentation"`
	PermissionProfile string   `json:"permission_profile"`
	ApprovalPolicy    string   `json:"approval_policy,omitempty"`
	Execution         string   `json:"execution"`
}

type ServerProfile struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	SSHAlias      string `json:"ssh_alias"`
	SSHPort       int    `json:"ssh_port,omitempty"`
	RemoteDir     string `json:"remote_dir"`
	DeployCommand string `json:"deploy_command,omitempty"`
	IdentityFile  string `json:"identity_file,omitempty"`
}

var referenceID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var sshAlias = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,252}$`)
var sshUser = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,63}$`)
var sshZone = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,32}$`)

func failure(code string) error { return errors.New(code) }

// Normalize applies the visible default without changing profile identities.
func (c *Config) Normalize() {
	for i := range c.ExecutionProfiles {
		if c.ExecutionProfiles[i].Presentation == "" {
			c.ExecutionProfiles[i].Presentation = "visible"
		}
	}
}

func safeText(s string, max int) bool {
	return len(s) <= max && !strings.Contains(s, "PRIVATE KEY") && !strings.ContainsFunc(s, unicode.IsControl)
}
func absoluteLocal(s string) bool { return filepath.IsAbs(s) && safeText(s, 32768) }

func safeSSHDestination(destination string) bool {
	if len(destination) > 512 {
		return false
	}
	host := destination
	if user, rest, ok := strings.Cut(destination, "@"); ok {
		if !sshUser.MatchString(user) || strings.Contains(rest, "@") {
			return false
		}
		host = rest
	}
	if sshAlias.MatchString(host) {
		return true
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	}
	address, err := netip.ParseAddr(host)
	return err == nil && address.Is6() && (address.Zone() == "" || sshZone.MatchString(address.Zone()))
}

func (p ExecutionProfile) Validate() error {
	if !referenceID.MatchString(p.ID) || strings.TrimSpace(p.Name) == "" || !safeText(p.Name, 256) {
		return failure("invalid_execution_profile")
	}
	if !absoluteLocal(p.ProjectDir) {
		return failure("invalid_project_dir")
	}
	if p.Presentation != "" && p.Presentation != "visible" && p.Presentation != "background" {
		return failure("invalid_presentation")
	}
	// Official built-in permission profile IDs start with a colon. They are
	// still references, checked against the effective inventory before a turn.
	if !referenceID.MatchString(strings.TrimPrefix(p.PermissionProfile, ":")) {
		return failure("invalid_permission_profile")
	}
	if p.Execution != "terminal" && p.Execution != "computer_use" {
		return failure("invalid_execution")
	}
	if p.ApprovalPolicy != "" && p.ApprovalPolicy != "on-request" && p.ApprovalPolicy != "never" {
		return failure("invalid_approval_policy")
	}
	if p.ApprovalPolicy == "never" && p.Execution != "terminal" {
		return failure("invalid_approval_policy")
	}
	if len(p.AllowedApps) > 256 || (p.Execution == "computer_use" && len(p.AllowedApps) == 0) {
		return failure("invalid_allowed_apps")
	}
	for _, app := range p.AllowedApps {
		if strings.TrimSpace(app) == "" || !safeText(app, 512) {
			return failure("invalid_allowed_apps")
		}
	}
	return nil
}

func (p ServerProfile) Validate() error {
	if !referenceID.MatchString(p.ID) || strings.TrimSpace(p.Name) == "" || !safeText(p.Name, 256) {
		return failure("invalid_server_profile")
	}
	if !safeSSHDestination(p.SSHAlias) {
		return failure("invalid_ssh_alias")
	}
	if p.SSHPort < 0 || p.SSHPort > 65535 {
		return failure("invalid_ssh_port")
	}
	if p.RemoteDir != "" && (!strings.HasPrefix(p.RemoteDir, "/") || !safeText(p.RemoteDir, 32768)) {
		return failure("invalid_remote_dir")
	}
	if p.IdentityFile != "" && !absoluteLocal(p.IdentityFile) {
		return failure("invalid_identity_file")
	}
	if !safeText(p.DeployCommand, 4096) {
		return failure("invalid_deploy_command")
	}
	return nil
}

func (c Config) Validate() error {
	if c.Version != SchemaVersion {
		return failure("unsupported_connections_version")
	}
	if c.CodexExecutable != "" && !absoluteLocal(c.CodexExecutable) {
		return failure("invalid_codex_executable")
	}
	if len(c.ExecutionProfiles) > 256 || len(c.ServerProfiles) > 256 {
		return failure("too_many_profiles")
	}
	ids := map[string]bool{}
	for _, p := range c.ExecutionProfiles {
		if err := p.Validate(); err != nil {
			return err
		}
		if ids[p.ID] {
			return failure("duplicate_profile_id")
		}
		ids[p.ID] = true
	}
	for _, p := range c.ServerProfiles {
		if err := p.Validate(); err != nil {
			return err
		}
		if ids[p.ID] {
			return failure("duplicate_profile_id")
		}
		ids[p.ID] = true
	}
	return nil
}

func newID(prefix string) (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", failure("storage_unavailable")
	}
	return prefix + hex.EncodeToString(bytes[:]), nil
}
