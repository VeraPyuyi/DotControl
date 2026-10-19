package dotevents

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const fixtureCallbackURL = "https://callback.example/hook?opaque=ok"

var testKey = []byte("01234567890123456789012345678901")

func openTestManager(t *testing.T) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	m, e := Open(dir, testKey, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = m.SetEnabled(true); e != nil {
		t.Fatal(e)
	}
	return m, dir
}
func testSecret() string {
	return "whsec_" + base64.StdEncoding.EncodeToString([]byte("01234567890123456789012345678901"))
}
func testParams(p string) Params {
	return Params{Name: EventName, Arguments: Arguments{QueueID: p}, Delivery: Callback{Mode: "webhook", URL: fixtureCallbackURL, Secret: testSecret()}}
}
func withFixtureServerURL(t *testing.T, h http.HandlerFunc) (*Manager, string, func(), string) {
	t.Helper()
	s := httptest.NewTLSServer(h)
	pool := x509.NewCertPool()
	pool.AddCert(s.Certificate())
	tr := s.Client().Transport.(*http.Transport).Clone()
	tr.Proxy = nil
	tr.DisableKeepAlives = true
	tr.TLSClientConfig.RootCAs = pool
	// Explicit fixture routing and fixture certificate hostname, confined to tests.
	tr.TLSClientConfig.ServerName = "example.com"
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "callback.example:443" {
			return nil, errf("callback")
		}
		return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, network, s.Listener.Addr().String())
	}
	dir := t.TempDir()
	m, e := Open(dir, testKey, &Delivery{client: &http.Client{Timeout: maxHTTPTimeout, Transport: tr, CheckRedirect: noRedirect}})
	if e != nil {
		t.Fatal(e)
	}
	if e = m.SetEnabled(true); e != nil {
		t.Fatal(e)
	}
	return m, dir, s.Close, fixtureCallbackURL
}
