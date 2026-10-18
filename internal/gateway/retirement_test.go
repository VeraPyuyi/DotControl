package gateway

import (
	"context"
	"encoding/json"
	"github.com/dotcontrol/dotcontrol/internal/codexbridge"
	"github.com/dotcontrol/dotcontrol/internal/service"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGatewayRetiredFieldsReachIdempotencyGateAndNeverExecute(t *testing.T) {
	b, err := codexbridge.New(codexbridge.Config{DeviceID: "device-a", StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	h := Handler(backendFunc(func(ctx context.Context, method string, r service.Request) (any, error) {
		return b.Submit(ctx, *r.Codex)
	}))
	for _, fields := range []string{`"server_profile_id":"legacy"`, `"experiment_id":"legacy"`, `"server_profile_id":""`, `"experiment_id":null`} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "https://127.0.0.1/rpc", strings.NewReader(`{"method":"codex_submit","request":{"device_id":"device-a","codex":{"device_id":"device-a","request_id":"new","prompt":"task",`+fields+`}}}`)))
		var env Envelope
		if json.Unmarshal(w.Body.Bytes(), &env) != nil || env.Error != "feature_retired" {
			t.Fatalf("retired request: %s", w.Body.String())
		}
	}
}
func TestGatewayRejectsTrailingRequestBeforeDispatch(t *testing.T) {
	called := false
	h := Handler(backendFunc(func(context.Context, string, service.Request) (any, error) { called = true; return nil, nil }))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "https://127.0.0.1/rpc", strings.NewReader(`{"method":"devices","request":{}} {}`)))
	if w.Code != 400 || called {
		t.Fatalf("trailing request accepted: %d %v", w.Code, called)
	}
}
