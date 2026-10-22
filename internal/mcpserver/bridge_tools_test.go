package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/dotbridge"
	"github.com/dotcontrol/dotcontrol/internal/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type bridgeNoopBackend struct{}

func (bridgeNoopBackend) Call(context.Context, string, service.Request) (any, error) {
	return map[string]any{"ok": true}, nil
}

func openBridgeForMCPTest(t *testing.T) *dotbridge.Service {
	t.Helper()
	root := physicalTemp(t)
	vault := dotbridge.NewFakeVault()
	if err := dotbridge.EnsureStateKey(vault); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	svc, err := dotbridge.Open(ctx, dotbridge.Config{
		Root:     filepath.Join(root, "dot-bridge"),
		Listen:   "127.0.0.1:0",
		Vault:    vault,
		MCPQueue: "client.a",
		Paused:   func() bool { return false },
		Revoked:  func() bool { return false },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	if err := svc.Enable(); err != nil {
		t.Fatal(err)
	}
	_, path, err := svc.Pair("client.a")
	if err != nil {
		t.Fatal(err)
	}
	var file dotbridge.PairingFile
	if json.Unmarshal(mustReadFile(path), &file) != nil {
		t.Fatal("pairing file invalid")
	}
	return svc
}

func mustReadFile(path string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	return data
}

func mcpBridgeTestClient(t *testing.T, s *mcp.Server, ctx context.Context) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "bridge-test", Version: "1"}, nil)
	st, mem := mcp.NewInMemoryTransports()
	sess, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	cs, err := client.Connect(ctx, mem, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func TestNewWithBridgeAddsTaskTools(t *testing.T) {
	svc := openBridgeForMCPTest(t)
	s, err := NewWithBridge(bridgeNoopBackend{}, svc)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := mcpBridgeTestClient(t, s, ctx)
	list, err := client.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Tools) != 17 {
		t.Fatalf("tool count=%d want 17", len(list.Tools))
	}
}

// physicalTemp resolves the test's temporary root: macOS temporary directories
// sit under the system /var symlink, which the stores deliberately reject as
// an ancestor. Production symlink rejection is unchanged.
func physicalTemp(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
