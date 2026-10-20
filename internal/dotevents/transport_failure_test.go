package dotevents

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

type failingCallbackTransport struct{ err error }

func (f failingCallbackTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, f.err }
func TestCallbackDeadlineIsDistinctFromNetworkFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{{"timeout", context.DeadlineExceeded, "timeout"}, {"network", errors.New("private address or token must not escape"), "network"}} {
		t.Run(tc.name, func(t *testing.T) {
			d := NewDelivery(&http.Client{Transport: failingCallbackTransport{tc.err}})
			p := testParams("alice")
			err := d.verify(context.Background(), &subscriptionRecord{ID: "sub.test", Params: p, Secret: p.Delivery.Secret})
			if got := asEventsError(err).Code; got != tc.want {
				t.Fatalf("code %q want %q", got, tc.want)
			}
		})
	}
}
