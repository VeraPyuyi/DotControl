package mcpserver

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/dotevents"
)

const fixtureCallbackURL = "https://callback.example/hook"

// NewTestEventsDelivery provides the offline TLS callback used by the event
// wire tests. Production always uses the public HTTPS transport.
func NewTestEventsDelivery(t *testing.T) (*dotevents.Delivery, func()) {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 64*1024))
		var probe struct {
			Type      string `json:"type"`
			Challenge string `json:"challenge"`
		}
		_ = json.Unmarshal(body, &probe)
		if probe.Type == "verification" {
			if probe.Challenge == "" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"challenge": probe.Challenge})
			return
		}
		if r.Header.Get("webhook-id") == "" || r.Header.Get("webhook-signature") == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	tr := server.Client().Transport.(*http.Transport).Clone()
	tr.Proxy = nil
	tr.DisableKeepAlives = true
	tr.TLSClientConfig = &tls.Config{RootCAs: pool, ServerName: "example.com"} //nolint:gosec // offline fixture
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "callback.example:443" {
			return nil, &net.OpError{Op: "dial", Err: errFixtureDial}
		}
		return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	return dotevents.NewDelivery(&http.Client{
		Timeout:       5 * time.Second,
		Transport:     tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}), server.Close
}

var errFixtureDial = &net.AddrError{Err: "fixture destination rejected", Addr: "callback"}
