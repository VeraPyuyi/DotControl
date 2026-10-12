package connections

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const commandWaitDelay = 2 * time.Second

// Status contains only stable codes, never aliases, paths, commands or output.
type Status struct {
	Available bool   `json:"available"`
	Code      string `json:"code"`
}

type PreflightOptions struct {
	Runner   CommandRunner
	LookPath func(string) (string, error)
	Timeout  time.Duration
}

// PreflightSSH invokes system ssh with the user's local alias/key references.
// The remote command checks POSIX directory existence and search permission.
// It never executes DeployCommand or changes known_hosts.
func PreflightSSH(ctx context.Context, p ServerProfile, opts PreflightOptions) Status {
	if err := p.Validate(); err != nil {
		return Status{Code: err.Error()}
	}
	if ctx.Err() != nil {
		return Status{Code: "ssh_cancelled"}
	}
	if p.IdentityFile != "" && !readableIdentityReference(p.IdentityFile) {
		return Status{Code: "ssh_identity_unavailable"}
	}
	lookup := opts.LookPath
	if lookup == nil {
		lookup = exec.LookPath
	}
	binary, err := lookup("ssh")
	if err != nil || !absoluteLocal(binary) {
		return Status{Code: "ssh_unavailable"}
	}
	runner := opts.Runner
	if runner == nil {
		runner = runCommand
	}
	timeout := opts.Timeout
	if timeout <= 0 || timeout > 20*time.Second {
		timeout = 20 * time.Second
	}
	bounded, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := []string{"-T", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=10", "-o", "ConnectionAttempts=1", "-o", "NumberOfPasswordPrompts=0", "-o", "ForwardAgent=no", "-o", "ForwardX11=no", "-o", "ClearAllForwardings=yes", "-o", "PermitLocalCommand=no", "-o", "ControlMaster=no", "-o", "ControlPath=none", "-o", "ControlPersist=no"}
	args = append(args, "-o", "UpdateHostKeys=no", "-o", "PasswordAuthentication=no", "-o", "KbdInteractiveAuthentication=no", "-o", "PreferredAuthentications=publickey")
	args = append(args, "-o", "CheckHostIP=no", "-o", "ForkAfterAuthentication=no")
	if p.IdentityFile != "" {
		args = append(args, "-i", p.IdentityFile)
	}
	if p.SSHPort != 0 {
		args = append(args, "-p", strconv.Itoa(p.SSHPort))
	}
	quoted := "'" + strings.ReplaceAll(p.RemoteDir, "'", `'"'"'`) + "'"
	command := "if test -d " + quoted + " && test -x " + quoted + "; then exit 0; else exit 73; fi"
	if p.RemoteDir == "" {
		command = "exit 0"
	}
	args = append(args, "--", p.SSHAlias, command)
	result, err := runner(bounded, binary, args)
	if errors.Is(err, context.Canceled) || errors.Is(bounded.Err(), context.Canceled) {
		return Status{Code: "ssh_cancelled"}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(bounded.Err(), context.DeadlineExceeded) {
		return Status{Code: "ssh_timeout"}
	}
	output := strings.ToLower(result.Stderr)
	if strings.Contains(output, "identity file ") && strings.Contains(output, "not accessible") || strings.Contains(output, "load key ") && (strings.Contains(output, "invalid format") || strings.Contains(output, "error in libcrypto") || strings.Contains(output, "permission denied")) || strings.Contains(output, "unprotected private key file") {
		return Status{Code: "ssh_identity_unavailable"}
	}
	if err == nil && result.ExitCode == 0 {
		if p.RemoteDir == "" {
			return Status{Available: true, Code: "ssh_connected"}
		}
		return Status{Available: true, Code: "ssh_ready"}
	}
	code := "ssh_preflight_failed"
	switch {
	case strings.Contains(output, "remote host identification has changed"), strings.Contains(output, "offending ") && strings.Contains(output, "key"):
		code = "ssh_host_key_changed"
	case strings.Contains(output, "host key verification failed"), strings.Contains(output, "host key is known") && strings.Contains(output, "strict checking"):
		code = "ssh_unknown_host"
	case strings.Contains(output, "permission denied"), strings.Contains(output, "authentication failed"), strings.Contains(output, "no supported authentication methods"), strings.Contains(output, "too many authentication failures"):
		code = "ssh_auth_failed"
	case strings.Contains(output, "could not resolve hostname"), strings.Contains(output, "connection refused"), strings.Contains(output, "connection timed out"), strings.Contains(output, "network is unreachable"), strings.Contains(output, "no route to host"), strings.Contains(output, "connection reset"), strings.Contains(output, "connection closed"):
		code = "ssh_network_error"
	case result.ExitCode == 73:
		code = "ssh_remote_dir_unavailable"
	}
	return Status{Code: code}
}

// Verify only metadata and the ability to open the reference. Key bytes are
// never read here; system ssh alone owns key parsing and authentication.
func readableIdentityReference(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	opened, statErr := f.Stat()
	closeErr := f.Close()
	return statErr == nil && closeErr == nil && opened.Mode().IsRegular() && os.SameFile(info, opened)
}
