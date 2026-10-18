package driver

import (
	"context"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"testing"
)

// Locked cua-driver 0.34.1 ActionResult contract; no native driver is used.
func TestPinnedEffectContract(t *testing.T) {
	for _, tc := range []struct {
		effect      any
		state, code string
	}{
		{"confirmed", "succeeded", "effect_confirmed"}, {"none", "failed", "effect_none"},
		{"partial", "unknown", "effect_partial"}, {"refused", "needs_user", "effect_refused"},
		{"suspected_noop", "unknown", "effect_suspected_noop"}, {"future", "unknown", "effect_unknown"},
		{nil, "unknown", "effect_unknown"}, {true, "unknown", "effect_unknown"}, {[]any{"confirmed"}, "unknown", "effect_unknown"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			got := outcome(&mcp.CallToolResult{StructuredContent: map[string]any{"effect": tc.effect}})
			if got.State != tc.state || got.Code != tc.code {
				t.Fatalf("%v: %+v", tc.effect, got)
			}
		})
	}
	for _, r := range []*mcp.CallToolResult{nil, {}, {Content: []mcp.Content{&mcp.TextContent{Text: `{"effect":`}}}} {
		if got := outcome(r); got.State != "unknown" {
			t.Fatal(got)
		}
	}
}
func TestUncertainEffectsCannotBecomeSuccessFromWeakText(t *testing.T) {
	for _, effect := range []string{"partial", "refused", "suspected_noop"} {
		t.Run(effect, func(t *testing.T) {
			a, v := fixtureAction()
			a.Expect = "saved"
			f := &fakeCaller{respond: func(string, map[string]any) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{StructuredContent: map[string]any{"effect": effect, "elements": []any{map[string]any{"value": "saved"}}}}, nil
			}}
			d := &Cua{client: f}
			got := d.Execute(context.Background(), a, v, "background")
			if got.State == "succeeded" || len(f.calls) != 1 {
				t.Fatalf("effect %s promoted/replayed: %+v %v", effect, got, f.calls)
			}
		})
	}
}
