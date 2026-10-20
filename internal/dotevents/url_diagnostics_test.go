package dotevents

import (
	"context"
	"net/http"
	"testing"
)

func TestRejectedCallbackPersistsCategorizedDiagnostic(t *testing.T) {
	for _, url := range []string{"http://example.com/hook", "https://localhost/hook", "https://127.0.0.1/hook"} {
		t.Run(url, func(t *testing.T) {
			m, _ := openTestManager(t)
			p := testParams("alice")
			p.Delivery.URL = url
			_, err := m.Subscribe(context.Background(), "alice", p)
			if got := asEventsError(err).Code; got != "callback" {
				t.Fatalf("URL code %q, want callback", got)
			}
			st, err := m.Status("alice", "alice")
			if err != nil {
				t.Fatal(err)
			}
			if st.SubscribeFailure == nil || st.SubscribeFailure.Reason != "callback" || st.SubscribeFailure.Stage != "callback_url" {
				t.Fatalf("missing URL diagnostic: %#v", st)
			}
		})
	}
}
func TestCallbackTransportPreservesAddressRejection(t *testing.T) {
	d := NewDelivery(&http.Client{Transport: failingCallbackTransport{&Error{Code: "callback"}}})
	p := testParams("alice")
	err := d.verify(context.Background(), &subscriptionRecord{ID: "sub.test", Params: p, Secret: p.Delivery.Secret})
	if got := asEventsError(err).Code; got != "callback" {
		t.Fatalf("address rejection flattened to %q", got)
	}
}
