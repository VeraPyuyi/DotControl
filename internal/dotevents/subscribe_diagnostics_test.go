package dotevents

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestSubscribeFailureDiagnosticSurvivesReopenAndIsQueueScoped(t *testing.T) {
	m, dir, closeFixture, _ := withFixtureServerURL(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"challenge":"wrong"}`))
	})
	defer closeFixture()
	if _, err := m.Subscribe(context.Background(), "alice", testParams("alice")); err == nil {
		t.Fatal("wrong challenge accepted")
	}
	reopened, err := Open(dir, testKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	status, err := reopened.Status("alice", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if status.SubscribeFailure == nil || status.SubscribeFailure.Reason != "verification" || status.SubscribeFailure.Stage != "challenge" {
		t.Fatalf("missing failure: %#v", status)
	}
	other, err := reopened.Status("bob", "bob")
	if err != nil {
		t.Fatal(err)
	}
	if other.SubscribeFailure != nil {
		t.Fatal("another queue received failure diagnostic")
	}
	raw, _ := json.Marshal(status)
	for _, secret := range []string{testSecret(), "callback.example", "wrong"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("private data in diagnostic: %s", raw)
		}
	}
}
