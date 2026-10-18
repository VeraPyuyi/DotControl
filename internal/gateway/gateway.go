// Package gateway connects explicitly paired devices over pinned mutual TLS.
package gateway

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/model"
	"github.com/dotcontrol/dotcontrol/internal/service"
)

type SecretStore interface {
	Get(string) (string, error)
	Set(string, string) error
}
type Peer struct {
	DeviceID    string `json:"device_id"`
	Name        string `json:"name"`
	URL         string `json:"url,omitempty"`
	Role        string `json:"role"` // controller or device
	Certificate string `json:"certificate"`
}
type Pairing struct {
	Dir   string
	Vault SecretStore
	mu    sync.Mutex
}

func (p *Pairing) Identity(id string) (tls.Certificate, error) {
	certFile := filepath.Join(p.Dir, "device.crt")
	b, e := os.ReadFile(certFile)
	if e == nil {
		key, e := p.Vault.Get("tls-key")
		if e != nil {
			return tls.Certificate{}, e
		}
		return tls.X509KeyPair(b, []byte(key))
	}
	if !os.IsNotExist(e) {
		return tls.Certificate{}, e
	}
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return tls.Certificate{}, e
	}
	serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if e != nil {
		return tls.Certificate{}, e
	}
	tmpl := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: id}, DNSNames: []string{id}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().AddDate(1, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}, BasicConstraintsValid: true}
	der, e := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if e != nil {
		return tls.Certificate{}, e
	}
	private, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		return tls.Certificate{}, e
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if e = p.Vault.Set("tls-key", string(keyPEM)); e != nil {
		return tls.Certificate{}, e
	}
	if e = os.WriteFile(certFile, certPEM, 0600); e != nil {
		return tls.Certificate{}, e
	}
	return tls.X509KeyPair(certPEM, keyPEM)
}
func Certificate(data []byte) (*x509.Certificate, error) {
	b, _ := pem.Decode(data)
	if b == nil || b.Type != "CERTIFICATE" {
		return nil, errors.New("invalid_certificate")
	}
	return x509.ParseCertificate(b.Bytes)
}
func Fingerprint(c *x509.Certificate) string {
	h := sha256.Sum256(c.Raw)
	return hex.EncodeToString(h[:])
}
func (p *Pairing) Peers() ([]Peer, error) {
	b, e := os.ReadFile(filepath.Join(p.Dir, "peers.json"))
	if os.IsNotExist(e) {
		return []Peer{}, nil
	}
	if e != nil {
		return nil, e
	}
	var peers []Peer
	e = json.Unmarshal(b, &peers)
	return peers, e
}
func (p *Pairing) Add(peer Peer) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	cert, e := Certificate([]byte(peer.Certificate))
	if e != nil {
		return e
	}
	if peer.DeviceID == "" || cert.Subject.CommonName != peer.DeviceID {
		return errors.New("certificate_device_mismatch")
	}
	if e = cert.VerifyHostname(peer.DeviceID); e != nil {
		return errors.New("certificate_identity_invalid")
	}
	if peer.Role != "controller" && peer.Role != "device" {
		return errors.New("invalid_peer_role")
	}
	if peer.Role == "device" {
		if e = ValidateURL(peer.URL); e != nil {
			return e
		}
	}
	peers, e := p.Peers()
	if e != nil {
		return e
	}
	for _, existing := range peers {
		if existing.DeviceID == peer.DeviceID {
			return errors.New("peer_already_paired_revoke_first")
		}
	}
	return p.save(append(peers, peer))
}
func (p *Pairing) Revoke(id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	peers, e := p.Peers()
	if e != nil {
		return e
	}
	out := make([]Peer, 0)
	found := false
	for _, peer := range peers {
		if peer.DeviceID != id {
			out = append(out, peer)
		} else {
			found = true
		}
	}
	if !found {
		return errors.New("peer_not_found")
	}
	return p.save(out)
}
func (p *Pairing) save(peers []Peer) error {
	b, e := json.MarshalIndent(peers, "", "  ")
	if e != nil {
		return e
	}
	tmp := filepath.Join(p.Dir, "peers.json.tmp")
	if e = os.WriteFile(tmp, b, 0600); e != nil {
		return e
	}
	return os.Rename(tmp, filepath.Join(p.Dir, "peers.json"))
}
func (p *Pairing) trusted(c *x509.Certificate, role string) bool {
	peers, e := p.Peers()
	if e != nil {
		return false
	}
	now := time.Now()
	if now.Before(c.NotBefore) || now.After(c.NotAfter) {
		return false
	}
	for _, peer := range peers {
		pinned, e := Certificate([]byte(peer.Certificate))
		if e == nil && peer.Role == role && peer.DeviceID == c.Subject.CommonName && Fingerprint(pinned) == Fingerprint(c) {
			return true
		}
	}
	return false
}
func (p *Pairing) ServerTLS(cert tls.Certificate) *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, ClientAuth: tls.RequireAnyClientCert, VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
		if len(raw) != 1 {
			return errors.New("unpaired_controller")
		}
		c, e := x509.ParseCertificate(raw[0])
		if e != nil || !p.trusted(c, "controller") {
			return errors.New("unpaired_controller")
		}
		roots := x509.NewCertPool()
		roots.AddCert(c)
		_, e = c.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
		return e
	}}
}
func (p *Pairing) Guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) != 1 || !p.trusted(r.TLS.PeerCertificates[0], "controller") {
			http.Error(w, "unpaired_controller", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func privateHost(host string) bool {
	addr, e := netip.ParseAddr(host)
	if e != nil {
		return false
	}
	// Bind explicit tailnet addresses (or loopback for deterministic local tests).
	return addr.IsLoopback() || netip.MustParsePrefix("100.64.0.0/10").Contains(addr) || netip.MustParsePrefix("fd7a:115c:a1e0::/48").Contains(addr)
}
func ValidateListen(addr string) error {
	host, _, e := net.SplitHostPort(addr)
	if e != nil || !privateHost(host) {
		return errors.New("gateway_requires_explicit_tailnet_or_loopback_ip")
	}
	return nil
}
func ValidateURL(raw string) error {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || !privateHost(u.Hostname()) {
		return errors.New("peer_url_requires_https_tailnet_ip")
	}
	return nil
}

type Envelope struct {
	Value json.RawMessage `json:"value,omitempty"`
	Error string          `json:"error,omitempty"`
}

func Handler(backend service.Backend) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "POST required", 405)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var input struct {
			Method  string          `json:"method"`
			Request service.Request `json:"request"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		dec.DisallowUnknownFields()
		if dec.Decode(&input) != nil || dec.Decode(new(any)) != io.EOF {
			http.Error(w, "invalid_request", 400)
			return
		}
		switch input.Method {
		case "devices", "apps", "observe", "execute", "task", "cancel", "codex_submit", "codex_job", "codex_cancel", "codex_continue", "codex_projects", "codex_threads", "codex_read_thread":
		default:
			http.Error(w, "unknown_method", 400)
			return
		}
		value, e := backend.Call(r.Context(), input.Method, input.Request)
		env := Envelope{}
		if e != nil {
			env.Error = e.Error()
		} else {
			env.Value, _ = json.Marshal(value)
		}
		_ = json.NewEncoder(w).Encode(env)
	})
}

type Gateway struct {
	Pairing  *Pairing
	Identity tls.Certificate
}

func (g *Gateway) remote(ctx context.Context, peer Peer, method string, req service.Request) (any, error) {
	if e := ValidateURL(peer.URL); e != nil {
		return nil, e
	}
	cert, e := Certificate([]byte(peer.Certificate))
	if e != nil {
		return nil, e
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: peer.DeviceID, Certificates: []tls.Certificate{g.Identity}}, DisableKeepAlives: true}
	client := &http.Client{Transport: transport, Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect_refused") }}
	b, _ := json.Marshal(map[string]any{"method": method, "request": req})
	r, _ := http.NewRequestWithContext(ctx, "POST", peer.URL+"/rpc", bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	response, e := client.Do(r)
	if e != nil {
		return nil, errors.New("device_unreachable_or_unpaired")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, errors.New("device_access_refused")
	}
	var env Envelope
	if e = json.NewDecoder(io.LimitReader(response.Body, 24<<20)).Decode(&env); e != nil {
		return nil, errors.New("invalid_device_response")
	}
	if env.Error != "" {
		return nil, fmt.Errorf("device_error: %s", env.Error)
	}
	var v any
	if e = json.Unmarshal(env.Value, &v); e != nil {
		return nil, errors.New("invalid_device_response")
	}
	return v, nil
}
func (g *Gateway) Call(ctx context.Context, method string, req service.Request) (any, error) {
	peers, e := g.Pairing.Peers()
	if e != nil {
		return nil, e
	}
	if method == "devices" {
		devices := make([]model.Device, 0)
		// A paired device is only published after identity-bound live readback.
		// Offline peers remain visible with availability=false, not as usable agents.
		for _, peer := range peers {
			if peer.Role != "device" {
				continue
			}
			value, e := g.remote(ctx, peer, "devices", service.Request{})
			var ds []model.Device
			b, _ := json.Marshal(value)
			if e == nil && json.Unmarshal(b, &ds) == nil && len(ds) == 1 && ds[0].ID == peer.DeviceID {
				if ds[0].CodexProfiles != nil {
					ds[0].CodexProfiles.Servers = nil
				}
				devices = append(devices, ds[0])
			} else {
				devices = append(devices, model.Device{ID: peer.DeviceID, Name: peer.Name, Paused: true})
			}
		}
		return devices, nil
	}
	for _, peer := range peers {
		if peer.Role == "device" && peer.DeviceID == req.DeviceID {
			return g.remote(ctx, peer, method, req)
		}
	}
	return nil, errors.New("device_not_paired")
}
