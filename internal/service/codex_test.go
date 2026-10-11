package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/dotcontrol/dotcontrol/internal/codexbridge"
	"github.com/dotcontrol/dotcontrol/internal/engine"
	"github.com/dotcontrol/dotcontrol/internal/model"
)

type quietDriver struct{}

func (quietDriver) Apps(context.Context) (json.RawMessage, error) {
	return nil, errors.New("must not inspect apps")
}
func (quietDriver) Observe(context.Context, model.Target) (model.View, error) {
	return model.View{}, errors.New("must not inspect desktop")
}
func (quietDriver) SupportsBackground(model.Action, model.View) bool { return false }
func (quietDriver) Check(context.Context, model.View) error {
	return errors.New("must not call driver")
}
func (quietDriver) Execute(context.Context, model.Action, model.View, string) model.Outcome {
	panic("native execution must be unreachable")
}
func (quietDriver) Release(context.Context) error { return nil }

type quietMonitor struct{}

func (quietMonitor) Snapshot() model.Activity {
	return model.Activity{Available: true, IdleMillis: 120000}
}

func localForCodex(t *testing.T) Local {
	t.Helper()
	e, err := engine.New(engine.Config{DeviceID: "device-a", StateDir: t.TempDir()}, quietDriver{}, quietMonitor{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close() })
	return Local{Engine: e}
}
func TestLocalCodexIsExplicitlyUnsupportedWithoutStore(t *testing.T) {
	s := localForCodex(t)
	for _, method := range []string{"codex_submit", "codex_job", "codex_cancel"} {
		v, err := s.Call(context.Background(), method, Request{DeviceID: "device-a"})
		if err != nil {
			t.Fatal(err)
		}
		u, ok := v.(model.CodexUnsupported)
		if !ok || u.Code != "codex_bridge_not_configured" {
			t.Fatalf("unsupported=%+v", v)
		}
	}
	v, err := s.Call(context.Background(), "devices", Request{})
	if err != nil {
		t.Fatal(err)
	}
	caps := v.([]model.Device)[0].Capabilities
	if caps.Base.Available || caps.Base.State != "not_probed" || caps.CodexConnection.Available || caps.OfficialComputerUse.Available || caps.CodexDelegation.Available {
		t.Fatalf("capabilities=%+v", caps)
	}
}
func TestLocalOwnsJobsAndNeverExecutesNativeInput(t *testing.T) {
	s := localForCodex(t)
	b, err := codexbridge.New(codexbridge.Config{DeviceID: "device-a", StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	s.Bridge = b
	r := model.CodexSubmit{DeviceID: "device-a", RequestID: "req-1", Prompt: "Perform bounded task", AllowedApps: []string{"Calculator"}, WorkingDirs: []string{t.TempDir()}}
	v, err := s.Call(context.Background(), "codex_submit", Request{DeviceID: "device-a", Codex: &r})
	if err != nil {
		t.Fatal(err)
	}
	j := v.(model.CodexJob)
	if j.State != "blocked" {
		t.Fatalf("job=%+v", j)
	}
	for _, method := range []string{"codex_job", "codex_cancel"} {
		v, err = s.Call(context.Background(), method, Request{DeviceID: "device-a", JobID: j.JobID})
		if err != nil || v.(model.CodexJob).JobID != j.JobID {
			t.Fatalf("%s=%+v %v", method, v, err)
		}
		if _, err = s.Call(context.Background(), method, Request{DeviceID: "other", JobID: j.JobID}); err == nil || err.Error() != "wrong_device" {
			t.Fatalf("wrong device=%v", err)
		}
	}
	if _, err = s.Call(context.Background(), "codex_submit", Request{DeviceID: "device-a", Codex: &model.CodexSubmit{DeviceID: "other"}}); err == nil || err.Error() != "wrong_device" {
		t.Fatalf("forged device=%v", err)
	}
}
