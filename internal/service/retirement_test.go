package service

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/dotcontrol/dotcontrol/internal/model"
	"strings"
	"testing"
)

func TestRetiredSubmitRejectedWithoutConfiguredBridge(t *testing.T) {
	s := localForCodex(t)
	r := model.CodexSubmit{DeviceID: "device-a", RequestID: "legacy", Prompt: "task", ServerProfileID: "legacy"}
	_, err := s.Call(context.Background(), "codex_submit", Request{DeviceID: "device-a", Codex: &r})
	if err == nil || err.Error() != "feature_retired" {
		t.Fatalf("retired request: %v", err)
	}
}

func TestGatewayEnvelopePreservesEmptyRetiredFieldsWithoutChangingRequestHashShape(t *testing.T) {
	var r Request
	if err := Decode([]byte(`{"device_id":"device-a","codex":{"device_id":"device-a","request_id":"r","prompt":"p","experiment_id":null}}`), &r); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var oldPeer struct {
		DeviceID string             `json:"device_id"`
		Target   model.Target       `json:"target"`
		Codex    *model.CodexSubmit `json:"codex"`
	}
	legacyDecoder := json.NewDecoder(bytes.NewReader(raw))
	legacyDecoder.DisallowUnknownFields()
	if err = legacyDecoder.Decode(&oldPeer); err == nil || !strings.Contains(err.Error(), "retired_codex_request") {
		t.Fatalf("old peer would accept retired forwarding: %s %v", raw, err)
	}
	var restored Request
	if err = Decode(raw, &restored); err != nil || !restored.Codex.RetiredFieldsPresent {
		t.Fatalf("forwarding lost retired field: %s %v", raw, err)
	}
	raw, _ = json.Marshal(r.Codex)
	if string(raw) != `{"device_id":"device-a","request_id":"r","prompt":"p","allowed_apps":null,"working_dirs":null}` {
		t.Fatalf("historical hash shape changed: %s", raw)
	}
}
func TestServiceReviewEntryRemoved(t *testing.T) {
	s := localForCodex(t)
	_, err := s.Call(context.Background(), "codex_review", Request{DeviceID: "device-a"})
	if err == nil || err.Error() != "unknown_method" {
		t.Fatalf("review entry: %v", err)
	}
}
func TestTransportDecodeRejectsUnknownAndRetainsRetiredPresence(t *testing.T) {
	for _, body := range []string{`{"device_id":"device-a","unknown":true}`, `{"device_id":"device-a","codex":{"device_id":"device-a","unknown":true}}`, `{} {}`} {
		if Decode([]byte(body), &Request{}) == nil {
			t.Fatalf("accepted unknown JSON %s", body)
		}
	}
	for _, body := range []string{
		`{"codex":{"server_profile_id":""}}`,
		`{"codex":{"experiment_id":null}}`,
		`{"codex":{"Server_Profile_Id":""}}`,
		`{"codex":{"EXPERIMENT_ID":null}}`,
		`{"codex":{},"RETIRED_CODEX_REQUEST":true}`,
	} {
		var r Request
		if err := Decode([]byte(body), &r); err != nil || !r.Codex.RetiredFieldsPresent {
			t.Fatalf("retired field ignored %s: %v", body, err)
		}
	}
}
