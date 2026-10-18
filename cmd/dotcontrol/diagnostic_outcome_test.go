package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dotcontrol/dotcontrol/internal/codexbridge"
	"github.com/dotcontrol/dotcontrol/internal/config"
	"github.com/dotcontrol/dotcontrol/internal/engine"
	"github.com/dotcontrol/dotcontrol/internal/model"
	"github.com/dotcontrol/dotcontrol/internal/tunnel"
)

type diagnosticOutcomeDriver struct{ outcome model.Outcome }

func (d diagnosticOutcomeDriver) Apps(context.Context) (json.RawMessage, error) { return nil, nil }
func (d diagnosticOutcomeDriver) Observe(_ context.Context, target model.Target) (model.View, error) {
	return model.View{Target: target, Fingerprint: "fixture", Elements: []string{"field"}}, nil
}
func (d diagnosticOutcomeDriver) SupportsBackground(model.Action, model.View) bool { return true }
func (d diagnosticOutcomeDriver) Check(context.Context, model.View) error          { return nil }
func (d diagnosticOutcomeDriver) Execute(context.Context, model.Action, model.View, string) model.Outcome {
	return d.outcome
}
func (d diagnosticOutcomeDriver) Release(context.Context) error { return nil }

type diagnosticOutcomeMonitor struct{}

func (diagnosticOutcomeMonitor) Snapshot() model.Activity {
	return model.Activity{Available: true, IdleMillis: 30000}
}

func TestDiagnosticsExportPreservesExecutionCause(t *testing.T) {
	for _, tc := range []struct{ state, code string }{
		{"unknown", "effect_partial"}, {"needs_user", "effect_refused"},
		{"unknown", "effect_suspected_noop"}, {"unknown", "effect_unknown"},
		{"succeeded", "postcondition_verified"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			cfg := config.New("diagnostic-fixture", "")
			eng, err := engine.New(engine.Config{DeviceID: cfg.DeviceID, StateDir: t.TempDir()}, diagnosticOutcomeDriver{model.Outcome{State: tc.state, Code: tc.code, Message: "PRIVATE_NATIVE_DETAILS"}}, diagnosticOutcomeMonitor{})
			if err != nil {
				t.Fatal(err)
			}
			defer eng.Close()
			bridge, err := codexbridge.New(codexbridge.Config{DeviceID: cfg.DeviceID, StateDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			defer bridge.Close()
			owner := &runtimeOwner{cfg: cfg, eng: eng, bridge: bridge, sup: &tunnel.Supervisor{}}
			ctx := context.Background()
			obs, err := eng.Observe(ctx, model.Target{PID: 100, WindowID: "200"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := eng.Execute(ctx, model.Action{DeviceID: cfg.DeviceID, TaskID: "task-1", ActionID: "action-1", ObservationID: obs.ID, Target: obs.View.Target, Kind: "click", Args: json.RawMessage(`{"element_token":"field"}`), Mode: "background"}); err != nil {
				t.Fatal(err)
			}
			diagnostics, err := owner.Diagnostics(ctx)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(diagnostics)
			if err != nil {
				t.Fatal(err)
			}
			var exported struct {
				Schema   int `json:"schema"`
				Progress struct {
					State string `json:"state"`
					Code  string `json:"code"`
				} `json:"execution_progress"`
			}
			if err := json.Unmarshal(raw, &exported); err != nil {
				t.Fatal(err)
			}
			if exported.Schema != 1 || exported.Progress.State != tc.state || exported.Progress.Code != tc.code || strings.Contains(string(raw), "PRIVATE_") {
				t.Fatalf("export lost cause or leaked driver text: %s", raw)
			}
		})
	}
}
