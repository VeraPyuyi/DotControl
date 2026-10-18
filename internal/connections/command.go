package connections

import (
	"bytes"
	"context"
	"os/exec"
	"time"
)

const commandWaitDelay = 2 * time.Second

// CommandResult is internal evidence for classification, never a UI response.
type CommandResult struct {
	Stdout          string `json:"-"`
	Stderr          string `json:"-"`
	ExitCode        int    `json:"-"`
	StdoutTruncated bool   `json:"-"`
	StderrTruncated bool   `json:"-"`
}
type CommandRunner func(context.Context, string, []string) (CommandResult, error)

// cappedBuffer consumes excess output without retaining it or blocking a child.
type cappedBuffer struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (b *cappedBuffer) Len() int       { return b.buffer.Len() }
func (b *cappedBuffer) String() string { return b.buffer.String() }

func (b *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - b.Len()
	if n > remaining {
		b.truncated = true
	}
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = b.buffer.Write(p)
	}
	return n, nil
}
func runCommand(ctx context.Context, binary string, args []string) (CommandResult, error) {
	return runCommandWithStdoutLimit(ctx, binary, args, 16384)
}

func runCommandWithStdoutLimit(ctx context.Context, binary string, args []string, stdoutLimit int) (CommandResult, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	stdout := cappedBuffer{limit: stdoutLimit}
	stderr := cappedBuffer{limit: 16384}
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// Bound draining inherited output handles as well as the main process.
	cmd.WaitDelay = commandWaitDelay
	err := cmd.Run()
	result := CommandResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: -1, StdoutTruncated: stdout.truncated, StderrTruncated: stderr.truncated}
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	return result, err
}
