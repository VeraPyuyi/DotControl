package engine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotcontrol/dotcontrol/internal/model"
)

func TestDriverOutcomeCausesSurviveReceiptDiagnosticsAndRestart(t *testing.T) {
	for _, tc := range []struct{ state, code string }{
		{"succeeded", "effect_confirmed"}, {"failed", "effect_none"},
		{"unknown", "effect_partial"}, {"needs_user", "effect_refused"},
		{"unknown", "effect_suspected_noop"}, {"unknown", "effect_unknown"},
		{"succeeded", "postcondition_verified"}, {"unknown", "postcondition_unverified"},
		{"unknown", "postcondition_invalid"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			e, d, m, cfg := setup(t)
			d.execute = func(context.Context, model.Action, model.View, string) model.Outcome {
				return model.Outcome{State: tc.state, Code: tc.code, Message: "PRIVATE_NATIVE_DETAILS"}
			}
			a := action(t, e, "cause-1")
			r := execute(t, e, a)
			if r.State != tc.state || r.Code != tc.code {
				t.Errorf("lost driver cause: %+v, want %s/%s", r, tc.state, tc.code)
			}
			b, err := json.Marshal(e.Progress())
			if err != nil {
				t.Fatal(err)
			}
			var progress map[string]any
			if err := json.Unmarshal(b, &progress); err != nil {
				t.Fatal(err)
			}
			if progress["state"] != tc.state || progress["code"] != tc.code {
				t.Errorf("diagnostics lost receipt cause: %s", b)
			}
			if retry := execute(t, e, a); retry != r || d.calls.Load() != 1 {
				t.Fatalf("retry changed cause or repeated input: %+v", retry)
			}
			journal, err := os.ReadFile(filepath.Join(cfg.StateDir, "ledger.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(r)
			if strings.Contains(string(journal)+string(encoded)+string(b), "PRIVATE_") {
				t.Fatal("native details leaked")
			}
			if !strings.Contains(string(journal), tc.code) {
				t.Errorf("durable cause missing: %s", tc.code)
			}
			if err := e.Close(); err != nil {
				t.Fatal(err)
			}
			restartedDriver := &testDriver{}
			restarted, err := New(cfg, restartedDriver, m)
			if err != nil {
				t.Fatal(err)
			}
			defer restarted.Close()
			if retry := execute(t, restarted, a); retry != r || restartedDriver.calls.Load() != 0 {
				t.Fatalf("restart changed cause or replayed input: %+v", retry)
			}
		})
	}
}

func TestDriverCauseNeverPromotesUnconfirmedEffects(t *testing.T) {
	for _, tc := range []struct{ state, code, want string }{
		{"succeeded", "effect_partial", "unknown"},
		{"succeeded", "effect_refused", "needs_user"},
		{"succeeded", "effect_suspected_noop", "unknown"},
		{"succeeded", "effect_unknown", "unknown"},
		{"succeeded", "effect_none", "failed"},
		{"succeeded", "postcondition_unverified", "unknown"},
		{"succeeded", "postcondition_invalid", "unknown"},
		{"unknown", "postcondition_verified", "unknown"},
		{"unknown", "effect_confirmed", "unknown"},
	} {
		t.Run(tc.state+"/"+tc.code, func(t *testing.T) {
			e, d, _, _ := setup(t)
			d.execute = func(context.Context, model.Action, model.View, string) model.Outcome {
				return model.Outcome{State: tc.state, Code: tc.code}
			}
			r := execute(t, e, action(t, e, "inconsistent-1"))
			if r.State != tc.want || r.Code != tc.code {
				t.Fatalf("driver code promoted unconfirmed effect or lost cause: %+v", r)
			}
		})
	}
}

func TestDriverOutcomePrivateCodesStayRedacted(t *testing.T) {
	for _, tc := range []struct{ state, code string }{
		{"succeeded", "verified"}, {"failed", "driver_failed"},
		{"unknown", "execution_uncertain"}, {"unsupported", "driver_unsupported"},
		{"needs_user", "driver_needs_user"},
	} {
		t.Run(tc.state, func(t *testing.T) {
			e, d, _, _ := setup(t)
			d.execute = func(context.Context, model.Action, model.View, string) model.Outcome {
				return model.Outcome{State: tc.state, Code: "PRIVATE_CODE", Message: "PRIVATE_MESSAGE"}
			}
			r := execute(t, e, action(t, e, "private-1"))
			if r.State != tc.state || r.Code != tc.code || r.Message != "" {
				t.Fatalf("private code leaked or fallback changed: %+v", r)
			}
		})
	}
}

func TestDriverCauseCannotOverrideCancellationOrReleaseFailure(t *testing.T) {
	for _, failure := range []string{"cancelled", "release"} {
		t.Run(failure, func(t *testing.T) {
			e, d, _, _ := setup(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			d.execute = func(context.Context, model.Action, model.View, string) model.Outcome {
				if failure == "cancelled" {
					cancel()
				}
				return model.Outcome{State: "succeeded", Code: "postcondition_verified"}
			}
			want := "execution_uncertain"
			if failure == "release" {
				want = "release_uncertain"
				d.releaseFn = func(context.Context) error { return errors.New("private release error") }
			}
			r, err := e.Execute(ctx, action(t, e, "interrupted-1"))
			if err != nil || r.State != "unknown" || r.Code != want || r.Message != "" {
				t.Fatalf("cause hid interruption: %+v %v", r, err)
			}
		})
	}
}
