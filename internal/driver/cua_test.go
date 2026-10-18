package driver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeCaller struct {
	calls   []string
	args    []map[string]any
	respond func(string, map[string]any) (*mcp.CallToolResult, error)
}

func (f *fakeCaller) CallTool(_ context.Context, p *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	f.calls = append(f.calls, p.Name)
	b, _ := json.Marshal(p.Arguments)
	var a map[string]any
	_ = json.Unmarshal(b, &a)
	f.args = append(f.args, a)
	if f.respond != nil {
		return f.respond(p.Name, a)
	}
	return &mcp.CallToolResult{StructuredContent: map[string]any{"effect": "confirmed"}}, nil
}
func fixtureAction() (model.Action, model.View) {
	t := model.Target{PID: 100, WindowID: "200"}
	return model.Action{Target: t, Kind: "click", Args: json.RawMessage(`{"element_token":"s00000001:0"}`)}, model.View{Target: t, Elements: []string{"s00000001:0"}, CaptureID: "capture-test", Text: `{"window_title":"Untitled"}`}
}

func TestRefusesRetargetingAndStaleTokens(t *testing.T) {
	for _, raw := range []string{`{"pid":999,"element_token":"s00000001:0"}`, `{"scope":"desktop","x":1,"y":1}`, `{"element_token":"s00000002:0"}`, `{"x":-1,"y":3}`, `{"element_token":"s00000001:0","x":1,"y":3}`} {
		a, v := fixtureAction()
		a.Args = json.RawMessage(raw)
		fake := &fakeCaller{}
		d := &Cua{client: fake}
		if got := d.Execute(context.Background(), a, v, "background"); got.State != "failed" || len(fake.calls) != 0 {
			t.Fatalf("unsafe args dispatched: %s %+v", raw, got)
		}
	}
}
func TestBoundTargetAndCapture(t *testing.T) {
	a, v := fixtureAction()
	a.Args = json.RawMessage(`{"x":4,"y":5}`)
	fake := &fakeCaller{}
	d := &Cua{client: fake, toolParameters: map[string]map[string]bool{"click": {"capture_id": true}}}
	got := d.Execute(context.Background(), a, v, "background")
	if got.State != "succeeded" {
		t.Fatal(got)
	}
	args := fake.args[0]
	if args["pid"] != float64(100) || args["window_id"] != float64(200) || args["capture_id"] != "capture-test" || args["delivery_mode"] != "background" {
		t.Fatalf("binding missing: %v", args)
	}
}
func TestUnknownTextStopsBeforeNextCharacter(t *testing.T) {
	for _, mode := range []string{"foreground", "background"} {
		t.Run(mode, func(t *testing.T) {
			a, v := fixtureAction()
			a.Kind = "type_text"
			a.Args = json.RawMessage(`{"text":"中文"}`)
			fake := &fakeCaller{respond: func(string, map[string]any) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{StructuredContent: map[string]any{"effect": "unverifiable"}}, nil
			}}
			d := &Cua{client: fake}
			got := d.Execute(context.Background(), a, v, mode)
			if got.State != "unknown" || len(fake.calls) != 1 || fake.args[0]["text"] != "中" {
				t.Fatalf("continued after uncertainty: %+v %v", got, fake.args)
			}
		})
	}
}
func TestWindowsAtomicElementTextIsNotOverwrittenByChunks(t *testing.T) {
	a, v := fixtureAction()
	a.Kind = "type_text"
	a.Args = json.RawMessage(`{"element_token":"s00000001:0","text":"中文"}`)
	fake := &fakeCaller{}
	d := &Cua{client: fake, goos: "windows"}
	got := d.Execute(context.Background(), a, v, "foreground")
	if got.State != "succeeded" || len(fake.calls) != 1 || fake.args[0]["text"] != "中文" {
		t.Fatal(got, fake.args)
	}
}
func TestTakeoverStopsNextBackgroundCharacter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	a, v := fixtureAction()
	a.Kind = "type_text"
	a.Args = json.RawMessage(`{"text":"abc"}`)
	fake := &fakeCaller{respond: func(string, map[string]any) (*mcp.CallToolResult, error) {
		cancel()
		return &mcp.CallToolResult{StructuredContent: map[string]any{"effect": "confirmed"}}, nil
	}}
	d := &Cua{client: fake}
	got := d.Execute(ctx, a, v, "background")
	if got.State == "succeeded" || len(fake.calls) != 1 {
		t.Fatal("typing continued or false success", got, fake.calls)
	}
}
func TestTransportLossPoisonsFutureDispatch(t *testing.T) {
	a, v := fixtureAction()
	fake := &fakeCaller{respond: func(string, map[string]any) (*mcp.CallToolResult, error) { return nil, errors.New("response lost") }}
	d := &Cua{client: fake}
	for i := 0; i < 2; i++ {
		if got := d.Execute(context.Background(), a, v, "background"); got.State != "unknown" {
			t.Fatal(got)
		}
	}
	if len(fake.calls) != 1 {
		t.Fatal("sent after unresolved native response")
	}
}

type contextCallerFunc func(context.Context, *mcp.CallToolParams) (*mcp.CallToolResult, error)

func (f contextCallerFunc) CallTool(ctx context.Context, p *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	return f(ctx, p)
}

func TestCancelledReadDoesNotDispatchOrDisableNextQuery(t *testing.T) {
	calls := 0
	d := &Cua{client: contextCallerFunc(func(ctx context.Context, _ *mcp.CallToolParams) (*mcp.CallToolResult, error) {
		calls++
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return &mcp.CallToolResult{}, nil
	})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.Apps(ctx); err == nil {
		t.Fatal("cancelled request succeeded")
	}
	if calls != 0 {
		t.Fatalf("dispatched %d calls after cancellation", calls)
	}
	if _, err := d.Apps(context.Background()); err != nil {
		t.Fatalf("cancelled read disabled subsequent discovery: %v", err)
	}
}

func TestInterruptedReadDoesNotPoisonFutureReads(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	d := &Cua{client: contextCallerFunc(func(ctx context.Context, _ *mcp.CallToolParams) (*mcp.CallToolResult, error) {
		calls++
		if calls == 1 {
			cancel()
			return nil, ctx.Err()
		}
		return &mcp.CallToolResult{}, nil
	})}
	if _, err := d.Apps(ctx); err == nil {
		t.Fatal("interrupted request succeeded")
	}
	if _, err := d.Apps(context.Background()); err != nil {
		t.Fatalf("read-only cancellation permanently disabled driver: %v", err)
	}
	if calls != 3 {
		t.Fatalf("unexpected dispatches: %d", calls)
	}
}

func TestSDKReadCancellationKeepsSessionUsable(t *testing.T) {
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	started := make(chan struct{})
	var calls atomic.Int32
	server := mcp.NewServer(&mcp.Implementation{Name: "readonly-cancellation-test", Version: "1"}, nil)
	for _, name := range []string{"list_apps", "list_windows"} {
		server.AddTool(&mcp.Tool{Name: name, InputSchema: map[string]any{"type": "object"}}, func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if calls.Add(1) == 1 {
				close(started)
				<-ctx.Done()
				return nil, ctx.Err()
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "{}"}}}, nil
		})
	}
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "readonly-cancellation-test", Version: "1"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	d := &Cua{client: cs}
	requestCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := d.Apps(requestCtx); done <- err }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("read did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("SDK cancellation misclassified: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("cancelled read did not finish")
	}
	if _, err := d.Apps(ctx); err != nil {
		t.Fatalf("real SDK session remained poisoned: %v", err)
	}
	if calls.Load() != 3 {
		t.Fatalf("unexpected automatic retry: %d", calls.Load())
	}
}

func TestUncertainMutationStillBlocksReadAndWriteDispatch(t *testing.T) {
	a, v := fixtureAction()
	fake := &fakeCaller{respond: func(string, map[string]any) (*mcp.CallToolResult, error) {
		return nil, context.DeadlineExceeded
	}}
	d := &Cua{client: fake}
	if got := d.Execute(context.Background(), a, v, "background"); got.State != "unknown" {
		t.Fatal(got)
	}
	if _, err := d.Apps(context.Background()); err == nil {
		t.Fatal("read dispatched after unresolved native action")
	}
	if got := d.Execute(context.Background(), a, v, "background"); got.State != "unknown" {
		t.Fatal(got)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("replayed or sent after uncertain action: %v", fake.calls)
	}
}

func TestDriverDiagnosticPreservesSafeCauseAndRemainsResponsive(t *testing.T) {
	fake := &fakeCaller{respond: func(string, map[string]any) (*mcp.CallToolResult, error) {
		return nil, errors.New("PRIVATE_WINDOW_INPUT_OR_TOKEN")
	}}
	d := &Cua{client: fake}
	status, ok := any(d).(interface{ Diagnostic() map[string]any })
	if !ok {
		t.Fatal("driver has no safe diagnostic status")
	}
	_, _ = d.Apps(context.Background())
	if got := status.Diagnostic(); got["connected"] != false || got["reconnection_required"] != true {
		t.Fatalf("unhealthy driver still reported ready: %v", got)
	}
	before, _ := json.Marshal(status.Diagnostic())
	if strings.Contains(string(before), "PRIVATE") || !strings.Contains(string(before), "list_apps") || !strings.Contains(string(before), "transport_error") {
		t.Fatalf("unsafe or missing diagnostic: %s", before)
	}
	_, _ = d.Apps(context.Background())
	after, _ := json.Marshal(status.Diagnostic())
	if string(after) != string(before) {
		t.Fatal("reconnection_required hid the original failure")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	done := make(chan struct{})
	go func() { _ = status.Diagnostic(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("status blocked on in-flight driver operation")
	}
}

func TestReadCancellationDiagnosticSurvivesRecovery(t *testing.T) {
	first := true
	fake := &fakeCaller{respond: func(string, map[string]any) (*mcp.CallToolResult, error) {
		if first {
			first = false
			return nil, fmt.Errorf("private wrapper: %w", context.DeadlineExceeded)
		}
		return &mcp.CallToolResult{}, nil
	}}
	d := &Cua{client: fake}
	status, ok := any(d).(interface{ Diagnostic() map[string]any })
	if !ok {
		t.Fatal("driver has no safe diagnostic status")
	}
	_, _ = d.Apps(context.Background())
	if got := status.Diagnostic(); got["reconnection_required"] != false {
		t.Fatalf("read deadline required reconnect: %v", got)
	}
	if _, err := d.Apps(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := status.Diagnostic()
	b, _ := json.Marshal(got)
	if got["connected"] != true || !strings.Contains(string(b), "request_deadline") || strings.Contains(string(b), "private") {
		t.Fatalf("diagnostic lost or leaked its safe cause: %s", b)
	}
}
func TestReleaseDoesNotEndReusableSession(t *testing.T) {
	a, v := fixtureAction()
	fake := &fakeCaller{}
	d := &Cua{client: fake}
	for i := 0; i < 2; i++ {
		if got := d.Execute(context.Background(), a, v, "background"); got.State != "succeeded" {
			t.Fatal(got)
		}
		if e := d.Release(context.Background()); e != nil {
			t.Fatal(e)
		}
	}
	if len(fake.calls) != 2 {
		t.Fatal("unexpected session teardown", fake.calls)
	}
}
func TestExpectedTextCannotOverrideRefusalOrOldContent(t *testing.T) {
	for _, tc := range []struct {
		name, effect, expect string
		refused              bool
		post                 map[string]any
	}{
		{"unchanged_title", "unverifiable", "Untitled", false, map[string]any{"window_title": "Untitled"}},
		{"json_metadata", "unverifiable", "pid", false, map[string]any{"pid": 123}},
		{"refused_existing_text", "", "Done", true, map[string]any{"window_title": "Done"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, v := fixtureAction()
			a.Expect = tc.expect
			fake := &fakeCaller{respond: func(name string, _ map[string]any) (*mcp.CallToolResult, error) {
				if name == "get_window_state" {
					return &mcp.CallToolResult{StructuredContent: tc.post}, nil
				}
				p := map[string]any{"effect": tc.effect}
				if tc.refused {
					p["code"] = "background_unavailable"
				}
				return &mcp.CallToolResult{IsError: tc.refused, StructuredContent: p}, nil
			}}
			d := &Cua{client: fake}
			if got := d.Execute(context.Background(), a, v, "background"); got.State == "succeeded" {
				t.Fatal("false verified success")
			}
		})
	}
}
func TestCalculatorPostconditionPreservesDirectionalMarks(t *testing.T) {
	for _, tc := range []struct {
		name, effect, expect, before, want, code string
	}{
		{"omitted", "unverifiable", "", "\u200e72", "unknown", "effect_unknown"},
		{"plain_digits", "unverifiable", "56", "\u200e72", "unknown", "effect_unknown"},
		{"literal_escape", "unverifiable", `\u200e56`, "\u200e72", "unknown", "effect_unknown"},
		{"exact_ax_value", "unverifiable", "\u200e56", "\u200e72", "unknown", "effect_unknown"},
		{"unchanged_ax_value", "unverifiable", "\u200e56", "\u200e56", "unknown", "effect_unknown"},
		{"confirmed_omitted", "confirmed", "", "\u200e72", "succeeded", "effect_confirmed"},
		{"confirmed_plain_digits", "confirmed", "56", "\u200e72", "unknown", "postcondition_unverified"},
		{"confirmed_literal_escape", "confirmed", `\u200e56`, "\u200e72", "unknown", "postcondition_unverified"},
		{"confirmed_exact_ax_value", "confirmed", "\u200e56", "\u200e72", "succeeded", "effect_confirmed"},
		{"confirmed_unchanged_ax_value", "confirmed", "\u200e56", "\u200e56", "succeeded", "effect_confirmed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, v := fixtureAction()
			a.Expect = tc.expect
			before, err := json.Marshal(map[string]any{"tree_markdown": fmt.Sprintf("- AXStaticText = \"%s\"", tc.before)})
			if err != nil {
				t.Fatal(err)
			}
			v.Text = string(before)
			fake := &fakeCaller{respond: func(name string, args map[string]any) (*mcp.CallToolResult, error) {
				if name == "get_window_state" {
					if args["pid"] != float64(100) || args["window_id"] != float64(200) || args["include_screenshot"] != false {
						t.Fatalf("verification lost target binding: %v", args)
					}
					return &mcp.CallToolResult{StructuredContent: map[string]any{"tree_markdown": "- AXStaticText = \"\u200e7\u200e×\u200e8\"\n- AXStaticText = \"\u200e56\""}}, nil
				}
				return &mcp.CallToolResult{StructuredContent: map[string]any{"effect": tc.effect}}, nil
			}}
			d := &Cua{client: fake, goos: "darwin", treeFormat: true}
			if got := d.Execute(context.Background(), a, v, "background"); got.State != tc.want || got.Code != tc.code {
				t.Fatalf("postcondition %q: got %+v, want %s/%s", tc.expect, got, tc.want, tc.code)
			}
			clicks := 0
			for _, name := range fake.calls {
				if name == "click" {
					clicks++
				}
			}
			if clicks != 1 {
				t.Fatalf("action sent %d times", clicks)
			}
			wantCalls := 1
			if tc.expect != "" {
				wantCalls++
			}
			if len(fake.calls) != wantCalls {
				t.Fatalf("expected bounded fresh text read: %v", fake.calls)
			}
		})
	}
}

func TestWindowLookupIgnoresLegacyDuplicates(t *testing.T) {
	a, _ := fixtureAction()
	fake := &fakeCaller{respond: func(string, map[string]any) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{StructuredContent: map[string]any{"_legacy_windows": []any{map[string]any{"pid": 100, "window_id": 200}}, "windows": []any{map[string]any{"pid": 100, "window_id": 200, "bounds": map[string]any{"x": 1, "y": 1}}}}}, nil
	}}
	d := &Cua{client: fake}
	for i := 0; i < 50; i++ {
		if _, e := d.window(context.Background(), a.Target); e != nil {
			t.Fatal(e)
		}
	}
}

func TestMacElementMultiCharacterTextRequiresSetValue(t *testing.T) {
	for _, mode := range []string{"foreground", "background"} {
		for _, text := range []string{"ab", "中文", "e\u0301", "👩‍💻"} {
			t.Run(mode+"/"+text, func(t *testing.T) {
				a, v := fixtureAction()
				a.Kind = "type_text"
				a.Args, _ = json.Marshal(map[string]any{"element_token": "s00000001:0", "text": text})
				fake := &fakeCaller{}
				d := &Cua{client: fake, goos: "darwin"}
				got := d.Execute(context.Background(), a, v, mode)
				if got.State != "needs_user" || got.Code != "use_set_value" || got.Message != "Use set_value for multi-character element text on macOS." || len(fake.calls) != 0 {
					t.Fatalf("uninterruptible Mac text dispatched: outcome=%+v calls=%v", got, fake.calls)
				}
			})
		}
	}
}

func TestMacSingleCharacterElementTextRemainsBounded(t *testing.T) {
	for _, text := range []string{"a", "中", "🙂"} {
		t.Run(text, func(t *testing.T) {
			a, v := fixtureAction()
			a.Kind = "type_text"
			a.Args, _ = json.Marshal(map[string]any{"element_token": "s00000001:0", "text": text})
			fake := &fakeCaller{}
			d := &Cua{client: fake, goos: "darwin"}
			got := d.Execute(context.Background(), a, v, "foreground")
			if got.State != "succeeded" || len(fake.calls) != 1 || fake.args[0]["text"] != text {
				t.Fatalf("bounded character failed: outcome=%+v calls=%v", got, fake.args)
			}
		})
	}
}

func TestMacSetValueRetainsAtomicElementPath(t *testing.T) {
	a, v := fixtureAction()
	v.Text = `{"elements":[{"element_token":"s00000001:0","role":"AXTextField","enabled":true,"in_web_content":false}]}`
	a.Kind = "set_value"
	a.Args = json.RawMessage(`{"element_token":"s00000001:0","value":"完整文本"}`)
	fake := &fakeCaller{}
	d := &Cua{client: fake, goos: "darwin"}
	got := d.Execute(context.Background(), a, v, "background")
	if got.State != "succeeded" || len(fake.calls) != 1 || fake.calls[0] != "set_value" || fake.args[0]["value"] != "完整文本" {
		t.Fatalf("safe replacement path failed: outcome=%+v calls=%v", got, fake.args)
	}
}

func TestMacNativeTextSetValueUsesPinnedSchema(t *testing.T) {
	for _, role := range []string{"AXTextField", "AXTextArea"} {
		for _, mode := range []string{"background", "foreground"} {
			t.Run(role+"/"+mode, func(t *testing.T) {
				a, v := fixtureAction()
				a.Kind = "set_value"
				a.Args = json.RawMessage(`{"element_token":"s00000001:0","value":"native text"}`)
				b, _ := json.Marshal(map[string]any{"elements": []any{map[string]any{"element_token": "s00000001:0", "role": role, "enabled": true, "actions": []string{"AXConfirm"}}}})
				v.Text = string(b)
				fake := &fakeCaller{}
				d := &Cua{client: fake, goos: "darwin"}
				got := d.Execute(context.Background(), a, v, mode)
				if got.State != "succeeded" || len(fake.calls) != 1 {
					t.Fatalf("native AX value rejected: %+v %v", got, fake.calls)
				}
				if _, present := fake.args[0]["delivery_mode"]; present {
					t.Fatalf("unsupported delivery_mode sent to pinned Mac set_value: %v", fake.args[0])
				}
				if len(fake.args[0]) != 5 {
					t.Fatalf("unexpected Mac set_value fields: %v", fake.args[0])
				}
			})
		}
	}
}

func TestMacSetValueRefusesUnsafeOrUnprovenElementKinds(t *testing.T) {
	cases := []struct{ name, text string }{
		{"missing_elements", `{"window_title":"Text"}`},
		{"invalid_snapshot", `not-json`},
		{"different_token", `{"elements":[{"element_token":"s00000001:1","role":"AXTextField","enabled":true}]}`},
		{"popup", `{"elements":[{"element_token":"s00000001:0","role":"AXPopUpButton","enabled":true}]}`},
		{"web_text", `{"elements":[{"element_token":"s00000001:0","role":"AXTextField","enabled":true,"in_web_content":true}]}`},
		{"unknown_web_flag", `{"elements":[{"element_token":"s00000001:0","role":"AXTextField","enabled":true,"in_web_content":"false"}]}`},
		{"disabled", `{"elements":[{"element_token":"s00000001:0","role":"AXTextArea","enabled":false}]}`},
		{"display_only", `{"elements":[{"element_token":"s00000001:0","role":"AXTextField","enabled":true,"display_only":true}]}`},
		{"minimum", `{"elements":[{"element_token":"s00000001:0","role":"AXTextField","enabled":true,"min":0}]}`},
		{"maximum", `{"elements":[{"element_token":"s00000001:0","role":"AXTextField","enabled":true,"max":100}]}`},
		{"enabled_missing", `{"elements":[{"element_token":"s00000001:0","role":"AXTextArea"}]}`},
		{"increment", `{"elements":[{"element_token":"s00000001:0","role":"AXTextField","enabled":true,"actions":["AXIncrement"]}]}`},
		{"decrement", `{"elements":[{"element_token":"s00000001:0","role":"AXTextField","enabled":true,"actions":["decrement"]}]}`},
		{"invalid_actions", `{"elements":[{"element_token":"s00000001:0","role":"AXTextField","enabled":true,"actions":"AXConfirm"}]}`},
		{"duplicate_token", `{"elements":[{"element_token":"s00000001:0","role":"AXTextField","enabled":true},{"element_token":"s00000001:0","role":"AXTextArea","enabled":true}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, v := fixtureAction()
			a.Kind = "set_value"
			a.Args = json.RawMessage(`{"element_token":"s00000001:0","value":"native text"}`)
			v.Text = tc.text
			fake := &fakeCaller{}
			d := &Cua{client: fake, goos: "darwin"}
			got := d.Execute(context.Background(), a, v, "background")
			if got.State != "needs_user" || len(fake.calls) != 0 {
				t.Fatalf("unsafe value route dispatched: %+v %v", got, fake.calls)
			}
		})
	}
}

func TestMacSetValueRefusesNumericStringsAndNonStrings(t *testing.T) {
	for _, value := range []any{"1", "-2.5", "1e3", " 42 ", "NaN", "+NaN", "-NaN", "Infinity", "inf", "+inf", "-inf", "0x1p2", "1e9999", 7, nil} {
		t.Run(fmt.Sprint(value), func(t *testing.T) {
			a, v := fixtureAction()
			a.Kind = "set_value"
			a.Args, _ = json.Marshal(map[string]any{"element_token": "s00000001:0", "value": value})
			v.Text = `{"elements":[{"element_token":"s00000001:0","role":"AXTextField","enabled":true}]}`
			fake := &fakeCaller{}
			d := &Cua{client: fake, goos: "darwin"}
			got := d.Execute(context.Background(), a, v, "background")
			if got.State != "needs_user" || len(fake.calls) != 0 {
				t.Fatalf("numeric/coerced value route dispatched: %+v %v", got, fake.calls)
			}
		})
	}
}

func TestMacCoordinateScrollAndDragRequireCaptureBinding(t *testing.T) {
	for _, kind := range []string{"scroll", "drag"} {
		for _, mode := range []string{"background", "foreground"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				a, v := fixtureAction()
				a.Kind = kind
				if kind == "scroll" {
					a.Args = json.RawMessage(`{"x":1,"y":2,"direction":"down","amount":1}`)
				} else {
					a.Args = json.RawMessage(`{"from_x":1,"from_y":2,"to_x":3,"to_y":4}`)
				}
				fake := &fakeCaller{}
				d := &Cua{client: fake, goos: "darwin"}
				got := d.Execute(context.Background(), a, v, mode)
				if got.State != "needs_user" || len(fake.calls) != 0 {
					t.Fatalf("unbound coordinate action dispatched: %+v %v", got, fake.calls)
				}
			})
		}
	}
}

func TestMacClickRetainsCaptureBindingAndElementScrollRetainsMode(t *testing.T) {
	for _, kind := range []string{"click", "scroll"} {
		t.Run(kind, func(t *testing.T) {
			a, v := fixtureAction()
			a.Kind = kind
			if kind == "click" {
				a.Args = json.RawMessage(`{"x":1,"y":2}`)
			} else {
				a.Args = json.RawMessage(`{"element_token":"s00000001:0","direction":"down","amount":1}`)
			}
			fake := &fakeCaller{}
			d := &Cua{client: fake, goos: "darwin", toolParameters: map[string]map[string]bool{"click": {"capture_id": true}}}
			got := d.Execute(context.Background(), a, v, "background")
			if got.State != "succeeded" || len(fake.calls) != 1 || fake.args[0]["delivery_mode"] != "background" {
				t.Fatalf("supported schema regressed: %+v %v", got, fake.args)
			}
			if kind == "click" && fake.args[0]["capture_id"] != v.CaptureID {
				t.Fatal("click lost capture binding")
			}
		})
	}
}

func TestWindowsSetValueRetainsExistingSchema(t *testing.T) {
	a, v := fixtureAction()
	a.Kind = "set_value"
	a.Args = json.RawMessage(`{"element_token":"s00000001:0","value":"123"}`)
	fake := &fakeCaller{}
	d := &Cua{client: fake, goos: "windows"}
	got := d.Execute(context.Background(), a, v, "background")
	if got.State != "succeeded" || len(fake.calls) != 1 || fake.args[0]["delivery_mode"] != "background" || fake.args[0]["value"] != "123" {
		t.Fatalf("Windows UIA path regressed: %+v %v", got, fake.args)
	}
}

func TestCoordinateActionsRefuseMissingCaptureSchemaOnEveryPlatform(t *testing.T) {
	for _, platform := range []string{"windows", "darwin"} {
		for _, kind := range []string{"scroll", "drag"} {
			t.Run(platform+"/"+kind, func(t *testing.T) {
				a, v := fixtureAction()
				a.Kind = kind
				if kind == "scroll" {
					a.Args = json.RawMessage(`{"x":1,"y":2,"direction":"down"}`)
				} else {
					a.Args = json.RawMessage(`{"from_x":1,"from_y":2,"to_x":3,"to_y":4}`)
				}
				fake := &fakeCaller{}
				d := &Cua{client: fake, goos: platform}
				got := d.Execute(context.Background(), a, v, "background")
				if got.State != "needs_user" || len(fake.calls) != 0 {
					t.Fatalf("unsupported capture schema dispatched: %+v %v", got, fake.calls)
				}
			})
		}
	}
}

func TestPinnedVersionParsingRequiresExactRelease(t *testing.T) {
	for _, output := range []string{"0.34.1", "cua-driver 0.34.1\n", "cua-driver-rs v0.34.1+build.abc", "cua-driver 0.34.1 (git: 0c69d9a2)"} {
		if !isPinnedVersion([]byte(output)) {
			t.Errorf("valid fixed version rejected: %q", output)
		}
	}
	for _, output := range []string{"0.34.10", "cua-driver 0.34.10", "cua-driver 0.34.2 (compatible 0.34.1)", "cua-driver 0.34.1-dev", "cua-driver 0.34.1evil", "cua-driver 0.34.1.0", "cua-driver 0.34.1\nextra-private-output", "private-prefix 0.34.1", ""} {
		if isPinnedVersion([]byte(output)) {
			t.Errorf("nonpinned output accepted: %q", output)
		}
	}
}

func TestVersionOutputIsBounded(t *testing.T) {
	var output versionOutput
	n, err := output.Write([]byte(strings.Repeat("private", 1000)))
	if err != nil || n != 7000 || !output.overflow || len(output.data) > 1024 {
		t.Fatalf("unbounded version capture: len=%d overflow=%t write=%d err=%v", len(output.data), output.overflow, n, err)
	}
}

func TestToolDiscoveryFollowsPagesAndCaptureSupport(t *testing.T) {
	d := &Cua{}
	calls := 0
	err := d.discoverTools(context.Background(), func(_ context.Context, p *mcp.ListToolsParams) (*mcp.ListToolsResult, error) {
		calls++
		if calls == 1 {
			if p.Cursor != "" {
				t.Fatal("first cursor")
			}
			return &mcp.ListToolsResult{Tools: []*mcp.Tool{{Name: "get_window_state", InputSchema: map[string]any{"properties": map[string]any{"tree_format": map[string]any{"type": "string"}}}}}, NextCursor: "page-2"}, nil
		}
		if p.Cursor != "page-2" {
			t.Fatal("next cursor missing")
		}
		return &mcp.ListToolsResult{Tools: []*mcp.Tool{{Name: "scroll", InputSchema: map[string]any{"properties": map[string]any{"capture_id": map[string]any{"type": "string"}}}}}}, nil
	})
	if err != nil || calls != 2 || !d.treeFormat || !d.toolParameters["scroll"]["capture_id"] {
		t.Fatalf("discovery=%v calls=%d params=%v", err, calls, d.toolParameters)
	}
	fake := &fakeCaller{}
	d.client = fake
	d.goos = "windows"
	a, v := fixtureAction()
	a.Kind = "scroll"
	a.Args = json.RawMessage(`{"x":1,"y":2,"direction":"down"}`)
	got := d.Execute(context.Background(), a, v, "background")
	if got.State != "succeeded" || len(fake.calls) != 1 || fake.args[0]["capture_id"] != v.CaptureID {
		t.Fatalf("declared capture contract lost: %+v %v", got, fake.args)
	}
}

func TestToolDiscoveryRejectsCursorLoop(t *testing.T) {
	d := &Cua{}
	calls := 0
	err := d.discoverTools(context.Background(), func(context.Context, *mcp.ListToolsParams) (*mcp.ListToolsResult, error) {
		calls++
		return &mcp.ListToolsResult{NextCursor: "repeat"}, nil
	})
	if err == nil || calls != 2 {
		t.Fatalf("cursor loop accepted: calls=%d err=%v", calls, err)
	}
}
