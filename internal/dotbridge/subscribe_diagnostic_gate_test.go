package dotbridge

import (
	"github.com/dotcontrol/dotcontrol/internal/dotevents"
	"testing"
)

type diagnosticEvents struct {
	EventsAPI
	after func()
}

func (e diagnosticEvents) Status(string, string) (dotevents.Status, error) {
	if e.after != nil {
		e.after()
	}
	return dotevents.Status{SubscribeFailure: &dotevents.SubscribeFailure{At: "2026-10-21T00:00:00Z", Reason: "verification", Stage: "challenge"}}, nil
}
func TestSubscribeDiagnosticFinalAuthorization(t *testing.T) {
	for _, change := range []string{"none", "disable", "revoke"} {
		t.Run(change, func(t *testing.T) {
			s, _, id := openTestBridge(t)
			s.mu.Lock()
			delete(s.subscriptions, id)
			s.mu.Unlock()
			s.events = diagnosticEvents{EventsAPI: s.events, after: func() {
				switch change {
				case "disable":
					if err := s.Disable(); err != nil {
						t.Fatal(err)
					}
				case "revoke":
					if err := s.RevokeClient(id); err != nil {
						t.Fatal(err)
					}
				}
			}}
			st, err := s.StatusForClient(id)
			if err != nil {
				t.Fatal(err)
			}
			if (st.SubscribeFailure != nil) != (change == "none") {
				t.Fatalf("diagnostic authorization: %#v", st)
			}
		})
	}
}
