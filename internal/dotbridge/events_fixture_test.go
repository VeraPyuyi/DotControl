package dotbridge

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/dotevents"
)

const fixtureCallbackURL = "https://callback.example/hook?opaque=ok"

func testEventsSecret() string {
	return "whsec_" + base64.StdEncoding.EncodeToString([]byte("01234567890123456789012345678901"))
}

func testEventsParams(queueID string) dotevents.Params {
	return dotevents.Params{
		Name:      dotevents.EventName,
		Arguments: dotevents.Arguments{QueueID: queueID},
		Delivery: dotevents.Callback{
			Mode:   "webhook",
			URL:    fixtureCallbackURL,
			Secret: testEventsSecret(),
		},
	}
}

// NewTestEventsDelivery is for offline integration tests only.
func NewTestEventsDelivery(t *testing.T) (*dotevents.Delivery, func()) {
	t.Helper()
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 32*1024))
		if strings.Contains(string(body), "verification") {
			var req struct {
				Challenge string `json:"challenge"`
			}
			if json.Unmarshal(body, &req) != nil || req.Challenge == "" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"challenge": req.Challenge})
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	pool := x509.NewCertPool()
	pool.AddCert(s.Certificate())
	tr := s.Client().Transport.(*http.Transport).Clone()
	tr.Proxy = nil
	tr.DisableKeepAlives = true
	tr.TLSClientConfig.RootCAs = pool
	tr.TLSClientConfig.ServerName = "example.com"
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "callback.example:443" {
			return nil, &dotevents.Error{Code: "callback"}
		}
		return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, network, s.Listener.Addr().String())
	}
	return dotevents.NewDelivery(&http.Client{
		Timeout:       10 * time.Second,
		Transport:     tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}), s.Close
}

// VerifyTestSubscription exercises callback verification for readiness tests.
func VerifyTestSubscription(t *testing.T, svc *Service, queueID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sub, err := svc.EventsSubscribe(ctx, queueID, testEventsParams(queueID))
	if err != nil || !sub.CallbackVerified {
		t.Fatalf("subscribe verify failed: %v %#v", err, sub)
	}
}
