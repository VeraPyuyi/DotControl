package dotbridge

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

// The hook runs after the real queue read has released its lock and before
// status publication, so lifecycle calls also prove service.mu is not held.
type capacityInterceptQueue struct {
	QueueAPI
	after func()
	calls int
}

func (q *capacityInterceptQueue) Capacity() (dotqueue.CapacityStatus, error) {
	q.calls++
	out, err := q.QueueAPI.Capacity()
	if q.after != nil {
		q.after()
	}
	return out, err
}

func TestCapacityPublicationAccessChanges(t *testing.T) {
	for _, admin := range []bool{false, true} {
		for _, change := range []string{"none", "disabled_before", "disable", "disable_enable", "revoke", "pause", "access_revoke"} {
			t.Run(fmt.Sprintf("admin=%v/%s", admin, change), func(t *testing.T) {
				var paused, revoked atomic.Bool
				s, _, id := openTestBridgeWithGates(t, paused.Load, revoked.Load)
				q := &capacityInterceptQueue{QueueAPI: s.queue}
				s.queue = q
				q.after = func() {
					switch change {
					case "disable", "disable_enable":
						if err := s.Disable(); err != nil {
							t.Fatal(err)
						}
						if change == "disable_enable" {
							if err := s.Enable(); err != nil {
								t.Fatal(err)
							}
						}
					case "revoke":
						if err := s.RevokeClient(id); err != nil {
							t.Fatal(err)
						}
					case "pause":
						paused.Store(true)
					case "access_revoke":
						revoked.Store(true)
					}
				}
				if change == "disabled_before" {
					if err := s.Disable(); err != nil {
						t.Fatal(err)
					}
				}
				wantCapacity := change == "none" || (admin && change == "revoke")
				if admin {
					out, err := s.AdminStatus()
					if err != nil {
						t.Fatal(err)
					}
					if (out["capacity"] != nil) != wantCapacity {
						t.Fatalf("capacity publication: %#v", out)
					}
					if out["enabled"] != (change != "disable" && change != "disabled_before") {
						t.Fatalf("mixed enabled snapshot: %#v", out)
					}
				} else {
					out, err := s.StatusForClient(id)
					if err != nil {
						t.Fatal(err)
					}
					if (out.Capacity != nil) != wantCapacity {
						t.Fatalf("capacity publication: %#v", out)
					}
					if change == "revoke" && out.Paired {
						t.Fatalf("revoked still paired: %#v", out)
					}
				}
				if change == "disabled_before" && q.calls != 0 {
					t.Fatalf("disabled queue reads=%d", q.calls)
				}
			})
		}
	}
}

func TestCapacityBlockedPublication(t *testing.T) {
	for _, admin := range []bool{false, true} {
		for _, change := range []string{"disable", "revoke"} {
			t.Run(fmt.Sprintf("admin=%v/%s", admin, change), func(t *testing.T) {
				s, _, id := openTestBridge(t)
				readDone, release := make(chan struct{}), make(chan struct{})
				s.queue = &capacityInterceptQueue{QueueAPI: s.queue, after: func() { close(readDone); <-release }}
				type result struct {
					status StatusDTO
					admin  map[string]any
					err    error
				}
				done := make(chan result, 1)
				go func() {
					var r result
					if admin {
						r.admin, r.err = s.AdminStatus()
					} else {
						r.status, r.err = s.StatusForClient(id)
					}
					done <- r
				}()
				select {
				case <-readDone:
				case <-time.After(5 * time.Second):
					t.Fatal("capacity read blocked by service lock")
				}
				var err error
				if change == "disable" {
					err = s.Disable()
				} else {
					err = s.RevokeClient(id)
				}
				close(release)
				if err != nil {
					t.Fatal(err)
				}
				select {
				case r := <-done:
					if r.err != nil {
						t.Fatal(r.err)
					}
					if admin {
						if change == "disable" && (r.admin["enabled"] != false || r.admin["capacity"] != nil) {
							t.Fatalf("disabled admin: %#v", r.admin)
						}
						if change == "revoke" && (r.admin["paired"] != false || r.admin["capacity"] == nil) {
							t.Fatalf("authorized admin aggregate: %#v", r.admin)
						}
					} else if r.status.Capacity != nil || (change == "revoke" && r.status.Paired) {
						t.Fatalf("unauthorized publication: %#v", r.status)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("status did not finish")
				}
			})
		}
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
