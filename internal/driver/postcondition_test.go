package driver

import (
	"context"
	"github.com/dotcontrol/dotcontrol/internal/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"testing"
)

func TestStructuredPostconditions(t *testing.T) {
	for _, tc := range []struct{ name, kind, before, after, want string }{
		{"calculator", "property", "0", "4", "succeeded"},
		{"text changed", "changed", "old", "new", "succeeded"},
		{"text unchanged", "changed", "new", "new", "unknown"},
		{"form submitted", "task_result", "draft", "submitted", "succeeded"},
		{"save without evidence", "task_result", "draft", "saved", "unknown"},
		{"weak text", "text", "old", "new", "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, v := fixtureAction()
			a.Postconditions = []model.Postcondition{{Kind: tc.kind, Element: "field", Property: "value", Expected: tc.after, MaxWaitMillis: 20}}
			v.Text = `{"elements":[{"element_token":"field","value":"` + tc.before + `"}]}`
			f := &fakeCaller{respond: func(name string, _ map[string]any) (*mcp.CallToolResult, error) {
				if name != "get_window_state" {
					return &mcp.CallToolResult{StructuredContent: map[string]any{"effect": "confirmed"}}, nil
				}
				el := map[string]any{"element_token": "field", "value": tc.after}
				p := map[string]any{"elements": []any{el}}
				if tc.name == "form submitted" {
					p["task_result"] = map[string]any{"source": "application_state", "value": "submitted"}
				}
				return &mcp.CallToolResult{StructuredContent: p}, nil
			}}
			d := &Cua{client: f}
			got := d.Execute(context.Background(), a, v, "background")
			if got.State != tc.want {
				t.Fatalf("%+v", got)
			}
		})
	}
}
func TestPostconditionDoesNotRewriteUnknownOrReplay(t *testing.T) {
	a, v := fixtureAction()
	a.Postconditions = []model.Postcondition{{Kind: "property", Element: "field", Property: "value", Expected: "4", MaxWaitMillis: 5}}
	f := &fakeCaller{respond: func(string, map[string]any) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{StructuredContent: map[string]any{"effect": "future"}}, nil
	}}
	d := &Cua{client: f}
	if got := d.Execute(context.Background(), a, v, "background"); got.State != "unknown" || len(f.calls) != 1 {
		t.Fatal(got, f.calls)
	}
}
