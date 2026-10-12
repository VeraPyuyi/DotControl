package driver

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Cua 0.34.1's persistent daemon rejects ended public session labels even on
// a new MCP transport. Two clients must also be unable to end each other's
// session. Model that upstream lifecycle contract over real MCP connections.
func TestDriverReconnectAndIndependentClientsDoNotReuseEndedSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var mu sync.Mutex
	ended := map[string]bool{}
	reads := map[string]int{}
	server := mcp.NewServer(&mcp.Implementation{Name: "persistent-driver", Version: "1"}, nil)
	for _, name := range []string{"list_apps", "list_windows", "end_session"} {
		server.AddTool(&mcp.Tool{Name: name, InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args struct {
				Session string `json:"session"`
			}
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
				return nil, err
			}
			mu.Lock()
			defer mu.Unlock()
			if name == "end_session" {
				ended[args.Session] = true
			} else if ended[args.Session] || args.Session == "" {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "session has ended; tool call rejected"}}}, nil
			} else {
				reads[args.Session]++
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "{}"}}}, nil
		})
	}
	connect := func() *Cua {
		st, ct := mcp.NewInMemoryTransports()
		ss, err := server.Connect(ctx, st, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ss.Close() })
		cs, err := mcp.NewClient(&mcp.Implementation{Name: "dotcontrol", Version: "test"}, nil).Connect(ctx, ct, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cs.Close() })
		return &Cua{client: cs, session: cs}
	}
	first, independent := connect(), connect()
	for _, d := range []*Cua{first, independent} {
		if _, err := d.Apps(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := independent.Apps(ctx); err != nil {
		t.Fatalf("closing one client disabled another: %v", err)
	}
	reconnected := connect()
	if _, err := reconnected.Apps(ctx); err != nil {
		t.Fatalf("fresh connection reused ended session: %v", err)
	}
	_ = independent.Close()
	_ = reconnected.Close()
	mu.Lock()
	defer mu.Unlock()
	if len(reads) != 3 || len(ended) != 3 {
		t.Fatalf("sessions overlap or cleanup missing: reads=%d ended=%d", len(reads), len(ended))
	}
	for label, count := range reads {
		if !ended[label] || (count != 2 && count != 4) {
			t.Fatalf("unstable session or cleanup: reads=%d ended=%v", count, ended[label])
		}
	}
}
