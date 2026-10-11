package gateway

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/model"
	"github.com/dotcontrol/dotcontrol/internal/service"
)

type memoryVault map[string]string

func (v memoryVault) Get(k string) (string, error) {
	s, ok := v[k]
	if !ok {
		return "", errors.New("missing secret")
	}
	return s, nil
}
func (v memoryVault) Set(k, s string) error { v[k] = s; return nil }

type backendFunc func(context.Context, string, service.Request) (any, error)

func (f backendFunc) Call(c context.Context, m string, r service.Request) (any, error) {
	return f(c, m, r)
}
func identity(t *testing.T, id string) (*Pairing, tls.Certificate, string) {
	t.Helper()
	p := &Pairing{Dir: t.TempDir(), Vault: memoryVault{}}
	cert, err := p.Identity(id)
	if err != nil {
		t.Fatal(err)
	}
	return p, cert, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]}))
}
func addPeer(t *testing.T, p *Pairing, peer Peer) {
	t.Helper()
	if err := p.Add(peer); err != nil {
		t.Fatal(err)
	}
}
func tlsServer(t *testing.T, p *Pairing, cert tls.Certificate, h http.Handler) *httptest.Server {
	t.Helper()
	s := httptest.NewUnstartedServer(p.Guard(h))
	s.Config.ErrorLog = log.New(io.Discard, "", 0)
	s.TLS = p.ServerTLS(cert)
	s.StartTLS()
	t.Cleanup(s.Close)
	return s
}
func pinnedClient(t *testing.T, server tls.Certificate, client *tls.Certificate, id string) *http.Client {
	t.Helper()
	leaf, err := x509.ParseCertificate(server.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	cfg := &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: id}
	if client != nil {
		cfg.Certificates = []tls.Certificate{*client}
	}
	tr := &http.Transport{TLSClientConfig: cfg}
	t.Cleanup(tr.CloseIdleConnections)
	return &http.Client{Transport: tr, Timeout: 3 * time.Second}
}

func TestMutualTLSRejectsUnpairedAndRevokesExistingConnection(t *testing.T) {
	p, serverCert, _ := identity(t, "device-a")
	_, controllerCert, controllerPEM := identity(t, "controller-a")
	_, rogueCert, _ := identity(t, "controller-a")
	addPeer(t, p, Peer{DeviceID: "controller-a", Role: "controller", Certificate: controllerPEM})
	var admitted atomic.Int32
	s := tlsServer(t, p, serverCert, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { admitted.Add(1); _, _ = io.WriteString(w, "accepted") }))
	for _, tc := range []struct {
		name string
		cert *tls.Certificate
	}{{"missing", nil}, {"same identity but different key", &rogueCert}} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := pinnedClient(t, serverCert, tc.cert, "device-a").Get(s.URL)
			if res != nil {
				res.Body.Close()
			}
			if err == nil {
				t.Fatal("unpaired client completed mutual TLS")
			}
		})
	}
	c := pinnedClient(t, serverCert, &controllerCert, "device-a")
	res, err := c.Get(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("paired status=%d", res.StatusCode)
	}
	if err := p.Revoke("controller-a"); err != nil {
		t.Fatal(err)
	}
	reused := false
	req, _ := http.NewRequestWithContext(httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{GotConn: func(c httptrace.GotConnInfo) { reused = c.Reused }}), "GET", s.URL, nil)
	res, err = c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if !reused {
		t.Fatal("test did not exercise an already authenticated connection")
	}
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("revoked existing connection status=%d", res.StatusCode)
	}
	if admitted.Load() != 1 {
		t.Fatalf("backend admitted %d requests; want one", admitted.Load())
	}
}

func TestGatewayRoutesExactPairedDeviceAndReportsOffline(t *testing.T) {
	controllers, controllerCert, controllerPEM := identity(t, "controller-a")
	devices, deviceCert, devicePEM := identity(t, "device-a")
	addPeer(t, devices, Peer{DeviceID: "controller-a", Role: "controller", Certificate: controllerPEM})
	var calls atomic.Int32
	var mismatched atomic.Bool
	s := tlsServer(t, devices, deviceCert, Handler(backendFunc(func(ctx context.Context, method string, req service.Request) (any, error) {
		calls.Add(1)
		if method == "devices" {
			id := "device-a"
			if mismatched.Load() {
				id = "impostor"
			}
			return []model.Device{{ID: id, Name: "Device A", Nonce: "fresh-device-nonce", Activity: model.Activity{Available: true}}}, nil
		}
		if req.DeviceID != "device-a" {
			return nil, errors.New("wrong route")
		}
		return map[string]any{"method": method, "task_id": req.TaskID}, nil
	})))
	addPeer(t, controllers, Peer{DeviceID: "device-a", Name: "Device A", Role: "device", URL: s.URL, Certificate: devicePEM})
	g := &Gateway{Pairing: controllers, Identity: controllerCert}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	v, err := g.Call(ctx, "devices", service.Request{})
	if err != nil {
		t.Fatal(err)
	}
	ds := v.([]model.Device)
	if len(ds) != 1 || ds[0].ID != "device-a" || !ds[0].Activity.Available || ds[0].Nonce != "fresh-device-nonce" {
		t.Fatalf("unexpected live device: %#v", ds)
	}
	v, err = g.Call(ctx, "task", service.Request{DeviceID: "device-a", TaskID: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	if v.(map[string]any)["task_id"] != "task-1" {
		t.Fatalf("request not routed: %#v", v)
	}
	before := calls.Load()
	if _, err = g.Call(ctx, "apps", service.Request{DeviceID: "Device A"}); err == nil || err.Error() != "device_not_paired" {
		t.Fatalf("name must not route: %v", err)
	}
	if calls.Load() != before {
		t.Fatal("unpaired device reached backend")
	}
	mismatched.Store(true)
	v, err = g.Call(ctx, "devices", service.Request{})
	if err != nil {
		t.Fatal(err)
	}
	ds = v.([]model.Device)
	if len(ds) != 1 || ds[0].ID != "device-a" || !ds[0].Paused || ds[0].Activity.Available || ds[0].Nonce != "" {
		t.Fatalf("identity mismatch published usable device: %#v", ds)
	}
	s.Close()
	v, err = g.Call(ctx, "devices", service.Request{})
	if err != nil {
		t.Fatal(err)
	}
	ds = v.([]model.Device)
	if len(ds) != 1 || !ds[0].Paused || ds[0].Activity.Available || ds[0].ID != "device-a" {
		t.Fatalf("offline pairing lost or usable: %#v", ds)
	}
	if _, err = g.Call(ctx, "apps", service.Request{DeviceID: "device-a"}); err == nil {
		t.Fatal("offline call succeeded")
	}
}

func TestGatewayNeverFollowsRedirects(t *testing.T) {
	p, controllerCert, controllerPEM := identity(t, "controller-a")
	d, deviceCert, devicePEM := identity(t, "device-a")
	addPeer(t, d, Peer{DeviceID: "controller-a", Role: "controller", Certificate: controllerPEM})
	var followed atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { followed.Add(1) }))
	defer target.Close()
	s := tlsServer(t, d, deviceCert, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	addPeer(t, p, Peer{DeviceID: "device-a", Role: "device", URL: s.URL, Certificate: devicePEM})
	if _, err := (&Gateway{Pairing: p, Identity: controllerCert}).Call(context.Background(), "apps", service.Request{DeviceID: "device-a"}); err == nil {
		t.Fatal("redirect succeeded")
	}
	if followed.Load() != 0 {
		t.Fatal("redirect target was contacted")
	}
}

func TestPairingRequiresCertificateIdentityAndExplicitSecureURLs(t *testing.T) {
	p, _, cert := identity(t, "device-a")
	if err := p.Add(Peer{DeviceID: "device-b", Role: "device", URL: "https://100.64.0.1:9443", Certificate: cert}); err == nil {
		t.Fatal("mismatched certificate paired")
	}
	for _, url := range []string{"https://100.64.0.1:9443", "https://100.127.255.254", "https://[fd7a:115c:a1e0::1]:9443", "https://127.0.0.1:9443", "https://[::1]:9443"} {
		if err := ValidateURL(url); err != nil {
			t.Errorf("valid URL %q: %v", url, err)
		}
	}
	for _, url := range []string{"http://100.64.0.1:9443", "https://example.com", "https://localhost:9443", "https://192.168.1.1", "https://8.8.8.8", "https://0.0.0.0", "https://100.63.255.255", "https://100.128.0.1", "https://u:p@100.64.0.1", "https://100.64.0.1/rpc", "https://100.64.0.1?token=x", "https://100.64.0.1#x"} {
		if err := ValidateURL(url); err == nil {
			t.Errorf("unsafe URL accepted: %q", url)
		}
	}
	for _, addr := range []string{"0.0.0.0:9443", ":9443", "localhost:9443", "192.168.1.1:9443", "[::]:9443"} {
		if err := ValidateListen(addr); err == nil {
			t.Errorf("unsafe bind accepted: %q", addr)
		}
	}
}

func TestGatewayHandlerRejectsUnrecognizedRequests(t *testing.T) {
	var calls atomic.Int32
	h := Handler(backendFunc(func(context.Context, string, service.Request) (any, error) { calls.Add(1); return nil, nil }))
	for _, body := range []string{`{"method":"shell","request":{}}`, `{"method":"apps","request":{"device_id":"device-a","unrecognized":true}}`, `{"method":"apps","request":{},"extra":true}`, `not json`} {
		r := httptest.NewRequest("POST", "https://127.0.0.1/rpc", strings.NewReader(body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest {
			t.Errorf("status=%d for %s", w.Code, body)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("malformed request reached backend")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "https://127.0.0.1/rpc", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status=%d", w.Code)
	}
}
