package dotbridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotcontrol/dotcontrol/internal/dotqueue"
	"github.com/dotcontrol/dotcontrol/internal/service"
)

type noopBackend struct{}

func (noopBackend) Call(context.Context, string, service.Request) (any, error) {
	return map[string]any{"ok": true}, nil
}

func openTestBridge(t *testing.T) (*Service, string, string) {
	t.Helper()
	root := t.TempDir()
	vault := NewFakeVault()
	if err := EnsureStateKey(vault); err != nil {
		t.Fatal(err)
	}
	delivery, closeFixture := NewTestEventsDelivery(t)
	t.Cleanup(closeFixture)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	svc, err := Open(ctx, Config{
		Root:           filepath.Join(root, "dot-bridge"),
		Listen:         "127.0.0.1:0",
		Vault:          vault,
		MCPQueue:       "client.a",
		EventsDelivery: delivery,
		Paused:         func() bool { return false },
		Revoked:        func() bool { return false },
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
	VerifyTestSubscription(t, svc, "client.a")
	var file PairingFile
	if json.Unmarshal(mustRead(path), &file) != nil {
		t.Fatal("pairing file invalid")
	}
	return svc, file.Token, file.ClientID
}

func mustRead(path string) []byte {
	data, err := readFile(path)
	if err != nil {
		panic(err)
	}
	return data
}

func readFile(path string) ([]byte, error) {
	return readFileOS(path)
}

func TestBridgeAPIProbeClaimComplete(t *testing.T) {
	svc, token, clientID := openTestBridge(t)
	probe, err := svc.StartProbe(clientID)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := svc.ClaimTask(probe.QueueID, probe.JobID, "worker.1")
	if err != nil {
		t.Fatal(err)
	}
	job, err := svc.CompleteTask(probe.JobID, claim.Token, dotqueue.Result{Text: strings.TrimPrefix(claim.Submission.Messages[0].Content, "Reply with exactly this nonce: ")})
	if err != nil || job.State != dotqueue.StateCompleted {
		t.Fatalf("complete: %v %#v", err, job)
	}
	status, err := svc.StatusForClient(clientID)
	if err != nil || !status.Ready {
		t.Fatalf("ready status %#v err=%v", status, err)
	}
	handler := svc.Handler()
	req := httptest.NewRequest(http.MethodGet, "/dot-bridge/v1/status", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Host = "127.0.0.1:9317"
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status code %d", res.Code)
	}
}
