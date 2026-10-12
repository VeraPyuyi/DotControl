package mcpserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/model"
	"github.com/dotcontrol/dotcontrol/internal/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type backendFunc func(context.Context, string, service.Request) (any, error)

func (f backendFunc) Call(c context.Context, m string, r service.Request) (any, error) {
	return f(c, m, r)
}

func connect(t *testing.T, b service.Backend, transport string) (context.Context, *mcp.ClientSession) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	client := mcp.NewClient(&mcp.Implementation{Name: "integration-test", Version: "1"}, nil)
	var ct mcp.Transport
	if transport == "http" {
		s := httptest.NewServer(HTTP(b))
		t.Cleanup(s.Close)
		ct = &mcp.StreamableClientTransport{Endpoint: s.URL}
	} else {
		st, mem := mcp.NewInMemoryTransports()
		ss, err := New(b).Connect(ctx, st, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { ss.Close() })
		ct = mem
	}
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return ctx, cs
}

func TestSDKToolCatalogAndExecuteObjectRoundTrip(t *testing.T) {
	for _, transport := range []string{"memory", "http"} {
		t.Run(transport, func(t *testing.T) {
			received := make(chan service.Request, 1)
			ctx, cs := connect(t, backendFunc(func(ctx context.Context, method string, r service.Request) (any, error) {
				if method != "execute" {
					return nil, errors.New("unexpected backend method")
				}
				received <- r
				return model.Receipt{DeviceID: r.DeviceID, TaskID: r.Action.TaskID, ActionID: r.Action.ActionID, State: "observed"}, nil
			}), transport)
			if got := cs.InitializeResult().ProtocolVersion; got != "2026-07-28" {
				t.Fatalf("modern protocol=%s", got)
			}
			list, err := cs.ListTools(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, tool := range list.Tools {
				names = append(names, tool.Name)
				a := tool.Annotations
				if a == nil {
					t.Fatalf("%s missing annotations", tool.Name)
				}
				wantRead := tool.Name != "execute" && tool.Name != "cancel" && tool.Name != "codex_submit" && tool.Name != "codex_cancel" && tool.Name != "codex_continue" && tool.Name != "codex_review"
				if a.ReadOnlyHint != wantRead {
					t.Errorf("%s readonly=%v want=%v", tool.Name, a.ReadOnlyHint, wantRead)
				}
				if !a.IdempotentHint {
					t.Errorf("%s must describe stable-ID retry semantics", tool.Name)
				}
				if !wantRead && a.DestructiveHint != nil && !*a.DestructiveHint {
					t.Errorf("%s incorrectly promises no destructive updates", tool.Name)
				}
				if tool.Name == "execute" {
					encoded, err := json.Marshal(tool.InputSchema)
					if err != nil {
						t.Fatal(err)
					}
					var schema map[string]any
					if err = json.Unmarshal(encoded, &schema); err != nil {
						t.Fatal(err)
					}
					props := schema["properties"].(map[string]any)
					args := props["args"].(map[string]any)
					if args["type"] != "object" {
						t.Fatalf("execute args schema=%s", encoded)
					}
				}
			}
			sort.Strings(names)
			if strings.Join(names, ",") != "apps,cancel,codex_cancel,codex_continue,codex_job,codex_projects,codex_read_thread,codex_review,codex_submit,codex_threads,devices,execute,observe,task" {
				t.Fatalf("tool catalog=%v", names)
			}
			args := map[string]any{"device_id": "device-a", "task_id": "task-a", "action_id": "action-a", "observation_id": "obs-a", "target": map[string]any{"pid": 42, "window_id": "window-a"}, "kind": "type_text", "args": map[string]any{"text": "你好，DotControl。🚀", "element_token": "field-1"}, "mode": "background", "expect": "\u200e56"}
			result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "execute", Arguments: args})
			if err != nil {
				t.Fatal(err)
			}
			if result.IsError {
				t.Fatalf("execute failed: %#v", result.Content)
			}
			select {
			case got := <-received:
				if got.DeviceID != "device-a" || got.Action == nil {
					t.Fatalf("bad action envelope: %#v", got)
				}
				a := got.Action
				if a.DeviceID != got.DeviceID || a.TaskID != "task-a" || a.ActionID != "action-a" || a.ObservationID != "obs-a" || a.Target.PID != 42 || a.Target.WindowID != "window-a" || a.Mode != "background" || a.Expect != "\u200e56" {
					t.Fatalf("action fields lost: %#v", a)
				}
				var values map[string]any
				if err = json.Unmarshal(a.Args, &values); err != nil {
					t.Fatal(err)
				}
				if values["text"] != "你好，DotControl。🚀" || values["element_token"] != "field-1" {
					t.Fatalf("argument object changed: %s", a.Args)
				}
			case <-ctx.Done():
				t.Fatal("tool never reached backend")
			}
		})
	}
}

func TestSDKObservationContainsImageOnceAndMetadataWithoutBase64(t *testing.T) {
	data := []byte("unique fake image bytes for transport testing")
	encoded := base64.StdEncoding.EncodeToString(data)
	observation := model.Observation{ID: "observation-a", DeviceID: "device-a", View: model.View{Target: model.Target{PID: 42, WindowID: "window-a"}, Text: "窗口内容", Images: []model.Image{{Data: encoded, MIMEType: "image/png"}}}}
	for _, remoteShape := range []bool{false, true} {
		t.Run(map[bool]string{false: "typed", true: "gateway decoded JSON"}[remoteShape], func(t *testing.T) {
			ctx, cs := connect(t, backendFunc(func(ctx context.Context, method string, r service.Request) (any, error) {
				if method != "observe" || r.DeviceID != "device-a" || r.Target.PID != 42 {
					return nil, errors.New("wrong observation route")
				}
				if remoteShape {
					b, _ := json.Marshal(observation)
					var value any
					if err := json.Unmarshal(b, &value); err != nil {
						return nil, err
					}
					return value, nil
				}
				return observation, nil
			}), "http")
			result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "observe", Arguments: map[string]any{"device_id": "device-a", "target": map[string]any{"pid": 42, "window_id": "window-a"}}})
			if err != nil {
				t.Fatal(err)
			}
			if result.IsError {
				t.Fatalf("observation error: %#v", result.Content)
			}
			images, texts := 0, 0
			for _, content := range result.Content {
				switch c := content.(type) {
				case *mcp.ImageContent:
					images++
					if c.MIMEType != "image/png" || !bytes.Equal(c.Data, data) {
						t.Fatalf("image corrupted: %#v", c)
					}
				case *mcp.TextContent:
					texts++
					if strings.Contains(c.Text, encoded) {
						t.Fatal("base64 duplicated into text")
					}
					var meta model.Observation
					if err = json.Unmarshal([]byte(c.Text), &meta); err != nil {
						t.Fatal(err)
					}
					if meta.ID != "observation-a" || meta.View.Text != "窗口内容" || len(meta.View.Images) != 0 {
						t.Fatalf("observation metadata=%#v", meta)
					}
				default:
					t.Fatalf("unexpected content: %T", content)
				}
			}
			if images != 1 || texts != 1 {
				t.Fatalf("content image count=%d text count=%d", images, texts)
			}
			all, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Count(all, []byte(encoded)) != 1 {
				t.Fatalf("image must be encoded exactly once: %s", all)
			}
			structured, _ := json.Marshal(result.StructuredContent)
			if bytes.Contains(structured, []byte(encoded)) {
				t.Fatal("base64 duplicated into structuredContent")
			}
		})
	}
}

func TestSDKRejectsInvalidExecuteArgumentsBeforeBackend(t *testing.T) {
	var calls atomic.Int32
	ctx, cs := connect(t, backendFunc(func(context.Context, string, service.Request) (any, error) { calls.Add(1); return nil, nil }), "memory")
	for _, args := range []any{map[string]any{"device_id": "device-a"}, map[string]any{"device_id": "device-a", "task_id": "task-a", "action_id": "action-a", "observation_id": "obs-a", "target": map[string]any{"pid": 42, "window_id": "window-a"}, "kind": "type_text", "args": "not an object", "mode": "auto"}} {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "execute", Arguments: args})
		if err == nil && !res.IsError {
			t.Fatal("invalid action arguments accepted")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid arguments reached backend")
	}
}

func TestSDKBackendFailureBecomesToolError(t *testing.T) {
	ctx, cs := connect(t, backendFunc(func(context.Context, string, service.Request) (any, error) { return nil, errors.New("needs_user") }), "memory")
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "apps", Arguments: map[string]any{"device_id": "device-a"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || len(res.Content) != 1 {
		t.Fatalf("failure was not an MCP tool error: %#v", res)
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok || text.Text != "needs_user" {
		t.Fatalf("backend failure changed: %#v", res.Content)
	}
}

func TestHTTPLegacyInitializationAndToolCalls(t *testing.T) {
	s := httptest.NewServer(HTTP(backendFunc(func(ctx context.Context, method string, r service.Request) (any, error) {
		if method != "devices" {
			return nil, errors.New("unexpected method")
		}
		return []model.Device{{ID: "legacy-device"}}, nil
	})))
	defer s.Close()
	post := func(body string) map[string]json.RawMessage {
		t.Helper()
		r, err := http.NewRequest("POST", s.URL, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		r.Header.Set("MCP-Protocol-Version", "2025-11-25")
		client := &http.Client{Timeout: 3 * time.Second}
		res, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != 200 {
			t.Fatalf("legacy HTTP status=%d body=%s", res.StatusCode, data)
		}
		var rpc map[string]json.RawMessage
		if err = json.Unmarshal(data, &rpc); err != nil {
			t.Fatalf("invalid response %s: %v", data, err)
		}
		if len(rpc["error"]) != 0 {
			t.Fatalf("legacy JSON-RPC error: %s", data)
		}
		return rpc
	}
	init := post(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","clientInfo":{"name":"legacy-test","version":"1"},"capabilities":{}}}`)
	var initialized mcp.InitializeResult
	if err := json.Unmarshal(init["result"], &initialized); err != nil {
		t.Fatal(err)
	}
	if initialized.ProtocolVersion != "2025-11-25" {
		t.Fatalf("legacy protocol=%s", initialized.ProtocolVersion)
	}
	list := post(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	var tools mcp.ListToolsResult
	if err := json.Unmarshal(list["result"], &tools); err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 14 {
		t.Fatalf("legacy tools=%d", len(tools.Tools))
	}
	call := post(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"devices","arguments":{}}}`)
	if !bytes.Contains(call["result"], []byte("legacy-device")) {
		t.Fatalf("legacy tool output=%s", call["result"])
	}
}
