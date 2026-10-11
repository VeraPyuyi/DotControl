// Package driver limits Cua Driver to window-scoped, observed actions.
package driver

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/dotcontrol/dotcontrol/internal/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const PinnedVersion = "0.34.1"

var pinnedVersionPattern = regexp.MustCompile(`^(?:cua-driver(?:-rs)?[ \t]+)?v?` + regexp.QuoteMeta(PinnedVersion) + `(?:\+[A-Za-z0-9][A-Za-z0-9._-]*)?(?:[ \t]+\([A-Za-z0-9][A-Za-z0-9 ._:+-]{0,127}\))?$`)

func isPinnedVersion(output []byte) bool {
	return len(output) <= 1024 && pinnedVersionPattern.MatchString(strings.TrimSpace(string(output)))
}

type versionOutput struct {
	data     []byte
	overflow bool
}

func (v *versionOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := 1024 - len(v.data)
	if len(p) > remaining {
		v.overflow = true
		p = p[:remaining]
	}
	v.data = append(v.data, p...)
	return n, nil
}

func checkPinnedVersion(ctx context.Context, path string) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, path, "--version")
	var output versionOutput
	command.Stdout = &output
	command.Stderr = io.Discard
	if command.Run() != nil {
		return errors.New("driver_version_unavailable")
	}
	if output.overflow || !isPinnedVersion(output.data) {
		return errors.New("driver_version_mismatch")
	}
	return nil
}

type Caller interface {
	CallTool(context.Context, *mcp.CallToolParams) (*mcp.CallToolResult, error)
}
type Cua struct {
	client         Caller
	session        *mcp.ClientSession
	mu             sync.Mutex
	poisoned       bool
	treeFormat     bool
	goos           string
	toolParameters map[string]map[string]bool
	diagnosticMu   sync.Mutex
	diagnostic     diagnosticState
}

func New(ctx context.Context, path string) (*Cua, error) {
	if path == "" {
		return nil, errors.New("cua_driver_not_configured")
	}
	// The absent web-content flag and restricted AXValue route are interpreted
	// according to this exact release, before starting any native session.
	if err := checkPinnedVersion(ctx, path); err != nil {
		return nil, err
	}
	command := exec.Command(path, "mcp")
	command.Stderr = io.Discard
	client := mcp.NewClient(&mcp.Implementation{Name: "dotcontrol-driver", Version: model.Version}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
	if err != nil {
		return nil, errors.New("cua_driver_connection_failed")
	}
	d := &Cua{client: session, session: session, goos: runtime.GOOS}
	if err = d.discoverTools(ctx, session.ListTools); err != nil {
		session.Close()
		return nil, errors.New("driver_discovery_failed")
	}
	d.recordSuccess()
	go func() {
		_ = session.Wait()
		d.diagnosticMu.Lock()
		d.diagnostic.connected = false
		d.diagnostic.closed = true
		d.diagnosticMu.Unlock()
	}()
	return d, nil
}

func (d *Cua) discoverTools(ctx context.Context, list func(context.Context, *mcp.ListToolsParams) (*mcp.ListToolsResult, error)) error {
	d.toolParameters = map[string]map[string]bool{}
	seenCursors := map[string]bool{"": true}
	cursor := ""
	for {
		page, err := list(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil || page == nil {
			return errors.New("driver_discovery_failed")
		}
		for _, tool := range page.Tools {
			if tool == nil {
				return errors.New("driver_discovery_failed")
			}
			if _, duplicate := d.toolParameters[tool.Name]; duplicate {
				return errors.New("driver_discovery_failed")
			}
			b, err := json.Marshal(tool.InputSchema)
			var schema struct {
				Properties map[string]any `json:"properties"`
			}
			if err != nil || json.Unmarshal(b, &schema) != nil {
				return errors.New("driver_discovery_failed")
			}
			fields := map[string]bool{}
			for name := range schema.Properties {
				fields[name] = true
			}
			d.toolParameters[tool.Name] = fields
		}
		if page.NextCursor == "" {
			break
		}
		if seenCursors[page.NextCursor] || len(seenCursors) >= 256 {
			return errors.New("driver_discovery_failed")
		}
		cursor = page.NextCursor
		seenCursors[cursor] = true
	}
	d.treeFormat = d.toolParameters["get_window_state"]["tree_format"]
	return nil
}
func (d *Cua) Close() error {
	if d.session != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		d.mu.Lock()
		_, _ = d.call(ctx, "end_session", map[string]any{})
		d.mu.Unlock()
		return d.session.Close()
	}
	return nil
}
func (d *Cua) call(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
	if d.poisoned {
		return nil, errors.New("driver_reconnection_required")
	}
	if err := ctx.Err(); err != nil {
		d.recordFailure(name, err, false, false)
		return nil, err
	}
	args["session"] = "dotcontrol"
	r, err := d.client.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		// Cancelling an observation cannot leave native input in flight. Keep
		// the session available for a NEW read, without retrying this request.
		// Unknown transport failures and every uncertain mutation still fail
		// closed until the connection is reconstructed.
		if (name == "list_apps" || name == "list_windows" || name == "get_window_state") && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
			d.recordFailure(name, err, true, false)
			return nil, err
		}
		d.poisoned = true
		d.recordFailure(name, err, true, true)
		return nil, errors.New("driver_transport_failed")
	}
	if r == nil {
		d.recordFailure(name, errEmptyResponse, true, false)
		return nil, errors.New("driver_empty_response")
	}
	if r.IsError {
		d.recordFailure(name, errToolResponse, true, false)
	} else {
		d.recordSuccess()
	}
	return r, nil
}
func payload(r *mcp.CallToolResult) (map[string]any, error) {
	var obj map[string]any
	if r.StructuredContent != nil {
		b, e := json.Marshal(r.StructuredContent)
		if e == nil {
			e = json.Unmarshal(b, &obj)
		}
		if e == nil && obj != nil {
			return obj, nil
		}
	}
	for _, c := range r.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			if json.Unmarshal([]byte(t.Text), &obj) == nil && obj != nil {
				return obj, nil
			}
		}
	}
	return nil, errors.New("driver_structured_data_missing")
}
func (d *Cua) Apps(ctx context.Context) (json.RawMessage, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	a, e := d.call(ctx, "list_apps", map[string]any{})
	if e != nil {
		return nil, e
	}
	if a.IsError {
		return nil, errors.New("app_discovery_failed")
	}
	w, e := d.call(ctx, "list_windows", map[string]any{})
	if e != nil {
		return nil, e
	}
	if w.IsError {
		return nil, errors.New("window_discovery_failed")
	}
	return json.Marshal(map[string]any{"apps": a, "windows": w})
}
func targetArgs(t model.Target) (map[string]any, error) {
	n, e := strconv.ParseUint(t.WindowID, 10, 64)
	if e != nil || n == 0 || t.PID < 1 {
		return nil, errors.New("invalid_target")
	}
	return map[string]any{"pid": t.PID, "window_id": n}, nil
}
func (d *Cua) window(ctx context.Context, t model.Target) (string, error) {
	args, e := targetArgs(t)
	if e != nil {
		return "", e
	}
	delete(args, "window_id")
	r, e := d.call(ctx, "list_windows", args)
	if e != nil {
		return "", e
	}
	if r.IsError {
		return "", errors.New("window_not_available")
	}
	// Preserve JSON numbers when comparing HWND values.
	b, _ := json.Marshal(r.StructuredContent)
	var v any
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.UseNumber()
	_ = dec.Decode(&v)
	if v == nil {
		for _, c := range r.Content {
			if txt, ok := c.(*mcp.TextContent); ok {
				dec = json.NewDecoder(strings.NewReader(txt.Text))
				dec.UseNumber()
				if dec.Decode(&v) == nil {
					break
				}
			}
		}
	}
	var found map[string]any
	var walk func(any)
	walk = func(x any) {
		switch n := x.(type) {
		case map[string]any:
			if n["bounds"] != nil && fmt.Sprint(n["window_id"]) == t.WindowID && fmt.Sprint(n["pid"]) == strconv.Itoa(t.PID) {
				found = n
				return
			}
			for _, child := range n {
				walk(child)
			}
		case []any:
			for _, child := range n {
				walk(child)
			}
		}
	}
	walk(v)
	if found == nil {
		return "", errors.New("window_not_found")
	}
	if found["bounds"] == nil {
		return "", errors.New("window_bounds_missing")
	}
	bound := map[string]any{"pid": found["pid"], "window_id": found["window_id"], "bounds": found["bounds"], "title": found["title"], "minimized": found["minimized"]}
	data, _ := json.Marshal(bound)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}
func (d *Cua) Observe(ctx context.Context, t model.Target) (model.View, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	args, e := targetArgs(t)
	if e != nil {
		return model.View{}, e
	}
	before, e := d.window(ctx, t)
	if e != nil {
		return model.View{}, e
	}
	args["max_elements"] = 500
	if d.treeFormat {
		args["tree_format"] = "both"
	}
	args["max_image_dimension"] = 1600
	r, e := d.call(ctx, "get_window_state", args)
	if e != nil {
		return model.View{}, e
	}
	if r.IsError {
		return model.View{}, errors.New("observation_failed")
	}
	after, e := d.window(ctx, t)
	if e != nil || before != after {
		return model.View{}, errors.New("window_moved_during_observation")
	}
	p, e := payload(r)
	if e != nil {
		return model.View{}, e
	}
	v := model.View{Target: t, Fingerprint: after}
	if s, ok := p["capture_id"].(string); ok {
		v.CaptureID = s
	}
	b, _ := json.Marshal(p)
	v.Text = string(b)
	if elements, ok := p["elements"].([]any); ok {
		for _, x := range elements {
			if m, ok := x.(map[string]any); ok {
				if token, ok := m["element_token"].(string); ok {
					v.Elements = append(v.Elements, token)
				}
			}
		}
	}
	for _, c := range r.Content {
		if img, ok := c.(*mcp.ImageContent); ok {
			v.Images = append(v.Images, model.Image{Data: base64.StdEncoding.EncodeToString(img.Data), MIMEType: img.MIMEType})
		}
	}
	if len(v.Images) == 0 {
		return model.View{}, errors.New("screenshot_unavailable")
	}
	return v, nil
}
func (d *Cua) Check(ctx context.Context, v model.View) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	f, e := d.window(ctx, v.Target)
	if e != nil {
		return e
	}
	if f != v.Fingerprint {
		return errors.New("window_changed")
	}
	return nil
}

var allowed = map[string]map[string]bool{
	"click":     {"element_token": true, "x": true, "y": true, "button": true},
	"type_text": {"element_token": true, "text": true},
	"set_value": {"element_token": true, "value": true},
	"press_key": {"element_token": true, "key": true},
	"hotkey":    {"element_token": true, "keys": true},
	"scroll":    {"element_token": true, "direction": true, "amount": true, "by": true, "x": true, "y": true},
	"drag":      {"from_x": true, "from_y": true, "to_x": true, "to_y": true},
}

func validate(a model.Action, v model.View) (map[string]any, error) {
	fields, ok := allowed[a.Kind]
	if !ok {
		return nil, errors.New("unsupported_action")
	}
	var args map[string]any
	if json.Unmarshal(a.Args, &args) != nil || args == nil {
		return nil, errors.New("invalid_action_args")
	}
	for k := range args {
		if !fields[k] {
			return nil, errors.New("unsupported_argument")
		}
	}
	if len(a.Args) > 16384 || len(a.Expect) > 1024 {
		return nil, errors.New("action_too_large")
	}
	if token, ok := args["element_token"]; ok {
		str, ok := token.(string)
		if !ok {
			return nil, errors.New("invalid_element_token")
		}
		found := false
		for _, t := range v.Elements {
			if str == t {
				found = true
				break
			}
		}
		if !found {
			return nil, errors.New("stale_element_token")
		}
	}
	if a.Kind == "type_text" {
		s, ok := args["text"].(string)
		if !ok || !utf8.ValidString(s) || utf8.RuneCountInString(s) > 4096 {
			return nil, errors.New("invalid_text")
		}
	}
	if a.Kind == "set_value" {
		if _, ok := args["element_token"].(string); !ok {
			return nil, errors.New("element_required")
		}
	}
	if a.Kind == "hotkey" {
		keys, ok := args["keys"].([]any)
		if !ok || len(keys) == 0 || len(keys) > 4 {
			return nil, errors.New("invalid_keys")
		}
		for _, k := range keys {
			s, ok := k.(string)
			if !ok || len(s) > 32 {
				return nil, errors.New("invalid_key")
			}
		}
	}
	if a.Kind == "press_key" {
		s, ok := args["key"].(string)
		if !ok || len(s) > 32 || s == "" {
			return nil, errors.New("invalid_key")
		}
	}
	coordinate := false
	for _, k := range []string{"x", "y", "from_x", "from_y", "to_x", "to_y"} {
		if val, ok := args[k]; ok {
			n, ok := val.(float64)
			if !ok || n < 0 || n > 65535 {
				return nil, errors.New("invalid_coordinates")
			}
			coordinate = true
		}
	}
	if coordinate {
		if v.CaptureID == "" {
			return nil, errors.New("capture_required")
		}
		args["capture_id"] = v.CaptureID
	}
	if a.Kind == "click" {
		_, el := args["element_token"]
		_, x := args["x"]
		_, y := args["y"]
		if (el && (x || y)) || (!el && !(x && y)) {
			return nil, errors.New("ambiguous_click")
		}
	}
	if a.Kind == "drag" {
		for _, k := range []string{"from_x", "from_y", "to_x", "to_y"} {
			if _, ok := args[k]; !ok {
				return nil, errors.New("invalid_drag")
			}
		}
		args["duration_ms"] = 100
		args["steps"] = 4
	}
	target, e := targetArgs(a.Target)
	if e != nil {
		return nil, e
	}
	for k, v := range target {
		args[k] = v
	}
	return args, nil
}
func (d *Cua) SupportsBackground(a model.Action, v model.View) bool {
	// These tools explicitly guarantee background delivery or refusal. This is
	// eligibility to ATTEMPT, not a promise that a particular app accepts it.
	_, err := validate(a, v)
	return err == nil
}

func outcome(r *mcp.CallToolResult) model.Outcome {
	p, e := payload(r)
	if e != nil {
		return model.Outcome{State: "unknown"}
	}
	raw, _ := json.Marshal(p)
	if r.IsError {
		if strings.Contains(string(raw), "background_unavailable") || strings.Contains(string(raw), "permission") || strings.Contains(string(raw), "target_occluded") {
			return model.Outcome{State: "needs_user"}
		}
		return model.Outcome{State: "unknown"}
	}
	// ActionResult.effect is the documented evidence boundary. An accepted
	// transport call, ok:true, or an unverified screenshot is not success.
	if p["effect"] == "confirmed" {
		return model.Outcome{State: "succeeded"}
	}
	if p["effect"] == "none" {
		return model.Outcome{State: "failed"}
	}
	return model.Outcome{State: "unknown"}
}
func (d *Cua) Execute(ctx context.Context, a model.Action, v model.View, mode string) model.Outcome {
	d.mu.Lock()
	defer d.mu.Unlock()
	args, e := validate(a, v)
	if e != nil {
		return model.Outcome{State: "failed"}
	}
	if ctx.Err() != nil {
		return model.Outcome{State: "needs_user"}
	}
	platform := d.goos
	if platform == "" {
		platform = runtime.GOOS
	}
	_, hasElement := args["element_token"]
	if _, hasCapture := args["capture_id"]; hasCapture && !d.toolParameters[a.Kind]["capture_id"] {
		return model.Outcome{State: "needs_user", Code: "capture_binding_unavailable", Message: "This action does not support verified screenshot coordinates."}
	}
	if platform == "darwin" && a.Kind == "set_value" && !macNativeTextValue(args, v) {
		return model.Outcome{State: "needs_user", Code: "native_text_required", Message: "macOS set_value requires a verified enabled native text field and nonnumeric text."}
	}
	// macOS may fall back from AX element typing to an uninterrupted CGEvent
	// loop. Only one Unicode scalar is bounded here; replacement text must use
	// set_value. Windows keeps its atomic UIA element-value path.
	if platform == "darwin" && a.Kind == "type_text" && hasElement && utf8.RuneCountInString(args["text"].(string)) > 1 {
		return model.Outcome{State: "needs_user", Code: "use_set_value", Message: "Use set_value for multi-character element text on macOS."}
	}
	// Long foreground held gestures are not exposed until the driver supports
	// a verified interrupt-and-release contract for those platforms.
	if mode == "foreground" && a.Kind == "drag" {
		return model.Outcome{State: "needs_user"}
	}
	args["delivery_mode"] = mode
	if platform == "darwin" && a.Kind == "set_value" {
		// The pinned macOS schema has no delivery_mode. Only the allowlisted
		// single AXValue string write reaches this branch; never drop a binding
		// or allow a popup, numeric stepping, or Safari fallback to fit a schema.
		delete(args, "delivery_mode")
	}
	// Never abandon an in-flight native operation on JSON-RPC cancellation:
	// keep the engine's writer gate until the bounded call has settled.
	call := func(args map[string]any) model.Outcome {
		if ctx.Err() != nil {
			return model.Outcome{State: "unknown"}
		}
		opCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		r, err := d.call(opCtx, a.Kind, args)
		if err != nil {
			return model.Outcome{State: "unknown"}
		}
		return outcome(r)
	}
	var result model.Outcome
	if a.Kind == "type_text" && !hasElement {
		// A physical takeover prevents the next character. Atomic native calls
		// already issued cannot be undone, and are reported uncertain on takeover.
		for _, r := range args["text"].(string) {
			args["text"] = string(r)
			result = call(args)
			if ctx.Err() != nil {
				return model.Outcome{State: "unknown"}
			}
			if result.State != "succeeded" {
				return result
			}
		}
	} else {
		result = call(args)
	}
	if ctx.Err() != nil {
		return model.Outcome{State: "unknown"}
	}
	if result.State == "needs_user" || result.State == "failed" {
		return result
	}
	if a.Expect != "" {
		// A bounded fresh AX read confirms the caller's concrete expected content.
		verifyArgs, _ := targetArgs(a.Target)
		verifyArgs["include_screenshot"] = false
		if d.treeFormat {
			verifyArgs["tree_format"] = "both"
		}
		r, err := d.call(ctx, "get_window_state", verifyArgs)
		if err != nil || r.IsError {
			return model.Outcome{State: "unknown"}
		}
		p, err := payload(r)
		if err != nil {
			return model.Outcome{State: "unknown"}
		}
		var before map[string]any
		_ = json.Unmarshal([]byte(v.Text), &before)
		if containsVisibleText(p, a.Expect) && (result.State == "succeeded" || !containsVisibleText(before, a.Expect)) {
			return model.Outcome{State: "succeeded"}
		}
		return model.Outcome{State: "unknown"}
	}
	return result
}

func macNativeTextValue(args map[string]any, v model.View) bool {
	value, ok := args["value"].(string)
	if !ok {
		return false
	}
	numeric := strings.TrimSpace(value)
	special := strings.TrimPrefix(strings.TrimPrefix(strings.ToLower(numeric), "+"), "-")
	if special == "nan" || special == "inf" || special == "infinity" {
		return false
	}
	if _, err := strconv.ParseFloat(numeric, 64); err == nil || errors.Is(err, strconv.ErrRange) {
		return false
	}
	token, ok := args["element_token"].(string)
	if !ok {
		return false
	}
	var snapshot struct {
		Elements []map[string]any `json:"elements"`
	}
	if json.Unmarshal([]byte(v.Text), &snapshot) != nil {
		return false
	}
	var selected map[string]any
	for _, element := range snapshot.Elements {
		if element["element_token"] == token {
			if selected != nil {
				return false
			}
			selected = element
		}
	}
	if selected == nil || selected["enabled"] != true {
		return false
	}
	role := selected["role"]
	if role != "AXTextField" && role != "AXTextArea" {
		return false
	}
	for _, name := range []string{"in_web_content", "display_only"} {
		if flag, present := selected[name]; present && flag != false {
			return false
		}
	}
	for _, name := range []string{"min", "max"} {
		if _, present := selected[name]; present {
			return false
		}
	}
	if raw, present := selected["actions"]; present {
		actions, ok := raw.([]any)
		if !ok {
			return false
		}
		for _, rawAction := range actions {
			action, ok := rawAction.(string)
			if !ok {
				return false
			}
			lower := strings.ToLower(action)
			if strings.Contains(lower, "increment") || strings.Contains(lower, "decrement") {
				return false
			}
		}
	}
	return true
}

// Match only fields representing UI text, never JSON keys, process ids,
// snapshot ids, schema descriptions, or other transport metadata.
func containsVisibleText(p map[string]any, expected string) bool {
	if expected == "" {
		return false
	}
	if s, ok := p["window_title"].(string); ok && s == expected {
		return true
	}
	if es, ok := p["elements"].([]any); ok {
		for _, v := range es {
			if el, ok := v.(map[string]any); ok {
				for _, key := range []string{"label", "value"} {
					if s, ok := el[key].(string); ok && s == expected {
						return true
					}
				}
			}
		}
	}
	if tree, ok := p["tree_markdown"].(string); ok {
		for _, match := range visibleQuotes.FindAllStringSubmatch(tree, -1) {
			if match[1] == expected {
				return true
			}
		}
	}
	return false
}

var visibleQuotes = regexp.MustCompile(`"([^"\n]*)"`)

func (d *Cua) Release(ctx context.Context) error {
	// Exposed operations are balanced press/release calls, never raw key-down
	// or button-down tools. Wait for the in-flight operation to settle first.
	d.mu.Lock()
	defer d.mu.Unlock()
	// No raw down-state is exposed; the completed native call already released
	// its own inputs. Ending a named session here would permanently invalidate
	// it and make the next observation fail. Close ends it exactly once.
	return nil
}
