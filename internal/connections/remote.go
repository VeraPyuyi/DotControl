package connections

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type RemoteOptions struct {
	Runner   CommandRunner
	LookPath func(string) (string, error)
}

// RunRemote uses only saved local references and strict, noninteractive SSH.
// Callers must validate the bounded response protocol; exit zero is not proof.
// Neither stderr nor raw transport errors can escape this function.
func RunRemote(ctx context.Context, p ServerProfile, script string, opts RemoteOptions) ([]byte, error) {
	if p.Validate() != nil || len(script) > 60000 {
		return nil, failure("invalid_remote_request")
	}
	if p.IdentityFile != "" && !readableIdentityReference(p.IdentityFile) {
		return nil, failure("ssh_identity_unavailable")
	}
	lookup := opts.LookPath
	if lookup == nil {
		lookup = exec.LookPath
	}
	binary, e := lookup("ssh")
	if e != nil || !absoluteLocal(binary) {
		return nil, failure("ssh_unavailable")
	}
	runner := opts.Runner
	if runner == nil {
		runner = runCommand
	}
	args := []string{"-T", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=10", "-o", "ConnectionAttempts=1", "-o", "NumberOfPasswordPrompts=0", "-o", "ForwardAgent=no", "-o", "ForwardX11=no", "-o", "ClearAllForwardings=yes", "-o", "PermitLocalCommand=no", "-o", "ControlMaster=no", "-o", "ControlPath=none", "-o", "ControlPersist=no", "-o", "UpdateHostKeys=no", "-o", "PasswordAuthentication=no", "-o", "KbdInteractiveAuthentication=no", "-o", "PreferredAuthentications=publickey", "-o", "CheckHostIP=no", "-o", "ForkAfterAuthentication=no"}
	if p.IdentityFile != "" {
		args = append(args, "-o", "IdentitiesOnly=yes", "-i", p.IdentityFile)
	}
	if p.SSHPort != 0 {
		args = append(args, "-p", strconv.Itoa(p.SSHPort))
	}
	args = append(args, "--", p.SSHAlias, "sh -c "+"'"+strings.ReplaceAll(script, "'", `'"'"'`)+"'")
	bounded, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	result, e := runner(bounded, binary, args)
	if e != nil || result.ExitCode != 0 || result.StdoutTruncated || len(result.Stdout) > 8192 {
		return nil, failure("ssh_remote_unconfirmed")
	}
	return []byte(result.Stdout), nil
}
