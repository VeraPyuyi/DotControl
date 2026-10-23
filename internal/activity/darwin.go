//go:build darwin

package activity

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"

	"github.com/dotcontrol/dotcontrol/internal/model"
)

// Start launches the installed, stable-identity activity helper. It never
// requests or works around TCC grants. Setup owns the app bundle location.
func Start(ctx context.Context, helper string) (*Monitor, error) {
	m := &Monitor{helperFailed: true}
	if helper == "" {
		return m, errors.New("activity helper is not installed")
	}
	ctx, cancel := context.WithCancel(ctx)
	m.close = cancel
	cmd := exec.CommandContext(ctx, helper)
	cmd.Stderr = io.Discard
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return m, err
	}
	if err = cmd.Start(); err != nil {
		cancel()
		return m, err
	}
	m.helperFailed = false
	done := make(chan struct{})
	m.close = func() { cancel(); <-done }
	go func() {
		defer close(done)
		defer cmd.Wait()
		defer m.markHelperFailed()
		s := bufio.NewScanner(stdout)
		s.Buffer(make([]byte, 4096), 8192)
		for s.Scan() {
			var state struct {
				model.Activity
				Reason     string `json:"availability_reason"`
				Permission *bool  `json:"input_monitoring_granted"`
			}
			if json.Unmarshal(s.Bytes(), &state) == nil {
				m.updateSample(state.Activity, state.Reason, state.Permission)
			}
		}
	}()
	return m, nil
}
