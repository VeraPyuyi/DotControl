package dotbridge

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dotcontrol/dotcontrol/internal/dotqueue"
)

func TestAdminAndPairedStatusExposeCapacity(t *testing.T) {
	s, token, clientID := openTestBridge(t)
	api := httptest.NewServer(s.Handler())
	defer api.Close()

	admin, err := s.AdminStatus()
	if err != nil {
		t.Fatal(err)
	}
	cap := decodeCapacity(admin["capacity"])
	if cap.Limit != 100 || cap.Retained != 0 || cap.Remaining != 100 {
		t.Fatalf("empty admin capacity %#v", cap)
	}

	if _, err := s.StartProbe(clientID); err != nil {
		t.Fatal(err)
	}
	admin, err = s.AdminStatus()
	if err != nil {
		t.Fatal(err)
	}
	cap = decodeCapacity(admin["capacity"])
	if cap.Retained != 1 || cap.Remaining != 99 {
		t.Fatalf("probe retained admin capacity %#v", cap)
	}

	code, raw := listHTTPRequest(t, api, http.MethodGet, "/dot-bridge/v1/status", token, nil)
	var paired StatusDTO
	if code != http.StatusOK || json.Unmarshal(raw, &paired) != nil || paired.Capacity == nil {
		t.Fatalf("paired status: %d %s", code, raw)
	}
	if paired.Capacity.Retained != 1 || paired.Capacity.Remaining != 99 {
		t.Fatalf("paired capacity %#v", paired.Capacity)
	}
}

func TestCapacityFullIncludesProbesAndOmitsWhenDisabled(t *testing.T) {
	s, token, clientID := openTestBridge(t)
	readyListAPIClient(t, s, clientID)
	api := httptest.NewServer(s.Handler())
	defer api.Close()

	for i := 0; i < 98; i++ {
		_, err := s.queue.Submit(clientID, dotqueue.Submission{
			RequestID: fmt.Sprintf("fill.%d", i),
			SessionID: "session.fill",
			Mode:      dotqueue.ModeAnswer,
			Messages:  []dotqueue.Message{{Role: "user", Content: "x"}},
		})
		if err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
	}
	if _, err := s.StartProbe(clientID); err != nil {
		t.Fatal(err)
	}
	admin, err := s.AdminStatus()
	if err != nil {
		t.Fatal(err)
	}
	cap := decodeCapacity(admin["capacity"])
	if cap.Retained != 100 || cap.Remaining != 0 {
		t.Fatalf("full capacity %#v", cap)
	}

	if _, err := s.StartProbe(clientID); err == nil || !strings.Contains(err.Error(), "queue_full") {
		t.Fatalf("probe overflow: %v", err)
	}
	_, err = s.queue.Submit(clientID, dotqueue.Submission{
		RequestID: "overflow.req",
		SessionID: "session.overflow",
		Mode:      dotqueue.ModeAnswer,
		Messages:  []dotqueue.Message{{Role: "user", Content: "y"}},
	})
	if err == nil || !strings.Contains(err.Error(), "queue_full") {
		t.Fatalf("queue overflow: %v", err)
	}

	if err := s.Disable(); err != nil {
		t.Fatal(err)
	}
	admin, err = s.AdminStatus()
	if err != nil {
		t.Fatal(err)
	}
	if admin["capacity"] != nil {
		t.Fatalf("disabled admin leaked capacity %#v", admin)
	}
	code, raw := listHTTPRequest(t, api, http.MethodGet, "/dot-bridge/v1/status", token, nil)
	var paired StatusDTO
	if code != http.StatusOK || json.Unmarshal(raw, &paired) != nil {
		t.Fatalf("status: %d %s", code, raw)
	}
	if paired.Capacity != nil {
		t.Fatalf("disabled paired leaked capacity %#v", paired.Capacity)
	}
}

func decodeCapacity(v any) CapacityDTO {
	b, err := json.Marshal(v)
	if err != nil {
		return CapacityDTO{}
	}
	var out CapacityDTO
	_ = json.Unmarshal(b, &out)
	return out
}
