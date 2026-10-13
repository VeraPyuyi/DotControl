// Package onboard serves a loopback-only, authenticated local UI.
// The MCP/admin token endpoints keep their existing stricter guards.
package onboard

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/dotcontrol/dotcontrol/internal/connections"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"time"
)

//go:embed web/*
var files embed.FS

type Connect struct {
	Name       string `json:"name"`
	TunnelID   string `json:"tunnel_id"`
	RuntimeKey string `json:"runtime_key"`
}

// ConnectionsRequest has no credential-value fields. It is accepted exclusively
// by the authenticated same-origin local wizard, never the remote MCP service.
type ConnectionsRequest struct {
	Operation       string                        `json:"operation"`
	Executable      string                        `json:"executable,omitempty"`
	Execution       *connections.ExecutionProfile `json:"execution,omitempty"`
	Server          *connections.ServerProfile    `json:"server,omitempty"`
	ProfileID       string                        `json:"profile_id,omitempty"`
	ServerProfileID string                        `json:"server_profile_id,omitempty"`
	PublicKeyPath   string                        `json:"public_key_path,omitempty"`
	AuthorizationID string                        `json:"authorization_id,omitempty"`
}

type ConnectionsBackend interface {
	Connections(context.Context, ConnectionsRequest) (any, error)
}
type Backend interface {
	Status(context.Context) (any, error)
	Connect(context.Context, Connect) error
	Action(context.Context, string) (any, error)
	Diagnostics(context.Context) (any, error)
}
type session struct {
	CSRF        string
	Expires     time.Time
	HardExpires time.Time
}

const (
	launchLifetime      = 30 * time.Minute
	sessionIdleLifetime = 12 * time.Hour
	sessionMaxLifetime  = 24 * time.Hour
)

type Server struct {
	origin     string
	backend    Backend
	assets     fs.FS
	mu         sync.Mutex
	tickets    map[string]time.Time
	sessions   map[string]session
	cookieName string
	now        func() time.Time
}

func New(address string, b Backend) *Server {
	assets, _ := fs.Sub(files, "web")
	return NewWithAssets(address, b, assets)
}

func NewWithAssets(address string, b Backend, assets fs.FS) *Server {
	origin := "http://" + address
	hash := sha256.Sum256([]byte(origin))
	return &Server{origin: origin, backend: b, assets: assets, tickets: map[string]time.Time{}, sessions: map[string]session{}, cookieName: "dotcontrol_session_" + hex.EncodeToString(hash[:8]), now: time.Now}
}
func random() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// LaunchURL must only be delivered through an existing local authenticated
// channel or directly to the OS browser. The fragment never reaches HTTP logs.
func (s *Server) LaunchURL() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune()
	t := random()
	s.tickets[t] = s.now().Add(launchLifetime)
	return s.origin + "/onboard/#" + t
}
func (s *Server) prune() {
	for k, v := range s.tickets {
		if !s.now().Before(v) {
			delete(s.tickets, k)
		}
	}
	for k, v := range s.sessions {
		if !s.now().Before(v.Expires) || !s.now().Before(v.HardExpires) {
			delete(s.sessions, k)
		}
	}
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	if "http://"+r.Host != s.origin || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != s.origin) {
		http.Error(w, "origin_rejected", 403)
		return
	}
	if r.URL.Path == "/onboard/" || r.URL.Path == "/onboard/app.js" || r.URL.Path == "/onboard/connections.js" || r.URL.Path == "/onboard/reviews.js" || r.URL.Path == "/onboard/history.js" || r.URL.Path == "/onboard/style.css" {
		if r.Method != "GET" {
			http.Error(w, "method_rejected", 405)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/onboard/")
		if name == "" {
			name = "index.html"
		}
		b, err := fs.ReadFile(s.assets, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		switch name {
		case "index.html":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		case "app.js", "connections.js", "reviews.js", "history.js":
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		case "style.css":
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		}
		w.Write(b)
		return
	}
	if r.Method != "POST" || r.Header.Get("Origin") != s.origin || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		http.Error(w, "request_rejected", 403)
		return
	}
	maxBody := int64(16 << 10)
	if r.URL.Path == "/onboard/api/history" {
		maxBody = 1 << 20
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if r.URL.Path == "/onboard/api/session" {
		s.bootstrap(w, r)
		return
	}
	c, err := r.Cookie(s.cookieName)
	if err != nil {
		write(w, 401, map[string]string{"error": "open_from_dotcontrol"})
		return
	}
	s.mu.Lock()
	s.prune()
	sess, ok := s.sessions[c.Value]
	if !ok || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-DotControl-CSRF")), []byte(sess.CSRF)) != 1 {
		s.mu.Unlock()
		write(w, 403, map[string]string{"error": "session_rejected"})
		return
	}
	s.touchSession(c.Value, sess)
	s.mu.Unlock()
	var value any
	switch r.URL.Path {
	case "/onboard/api/history":
		var input HistoryRequest
		err = decode(r, &input)
		if err == nil {
			if backend, ok := s.backend.(HistoryBackend); ok {
				value, err = backend.History(r.Context(), input)
			} else {
				err = errors.New("history_unavailable")
			}
		}
	case "/onboard/api/reviews":
		var input ReviewRequest
		err = decode(r, &input)
		if err == nil {
			if backend, ok := s.backend.(ReviewsBackend); ok {
				value, err = backend.Reviews(r.Context(), input)
			} else {
				err = errors.New("reviews_unavailable")
			}
		}
	case "/onboard/api/connections":
		var input ConnectionsRequest
		err = decode(r, &input)
		if err == nil {
			if backend, ok := s.backend.(ConnectionsBackend); ok {
				value, err = backend.Connections(r.Context(), input)
			} else {
				err = errors.New("connections_unavailable")
			}
		}
	case "/onboard/api/status":
		value, err = s.backend.Status(r.Context())
	case "/onboard/api/connect":
		var input Connect
		err = decode(r, &input)
		if err == nil {
			err = s.backend.Connect(r.Context(), input)
		}
		input.RuntimeKey = ""
		value = map[string]bool{"saved": err == nil}
	case "/onboard/api/action":
		var input struct {
			Action string `json:"action"`
		}
		err = decode(r, &input)
		if err == nil {
			value, err = s.backend.Action(r.Context(), input.Action)
		}
	case "/onboard/api/diagnostics":
		value, err = s.backend.Diagnostics(r.Context())
		w.Header().Set("Content-Disposition", `attachment; filename="DotControl-diagnostics.json"`)
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		write(w, 400, map[string]string{"error": publicError(err)})
		return
	}
	write(w, 200, value)
}
func publicError(err error) string { // Backend errors must be stable codes, never raw subprocess output.
	code := err.Error()
	if len(code) == 0 || len(code) > 100 {
		return "operation_failed"
	}
	for _, r := range code {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
			return "operation_failed"
		}
	}
	return code
}
func decode(r *http.Request, out any) error {
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		return errors.New("invalid_input")
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("invalid_input")
	}
	return nil
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func (s *Server) bootstrap(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Ticket string `json:"ticket"`
	}
	if decode(r, &input) != nil {
		http.Error(w, "invalid_input", 400)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune()
	if input.Ticket == "" {
		if c, e := r.Cookie(s.cookieName); e == nil {
			if sess, ok := s.sessions[c.Value]; ok {
				s.sessionReply(w, c.Value, sess)
				return
			}
		}
	}
	if _, ok := s.tickets[input.Ticket]; !ok {
		write(w, 401, map[string]string{"error": "launch_expired_open_dotcontrol"})
		return
	}
	delete(s.tickets, input.Ticket)
	// A fresh launch in the same browser must not invalidate another tab's CSRF.
	if c, e := r.Cookie(s.cookieName); e == nil {
		if sess, ok := s.sessions[c.Value]; ok {
			s.sessionReply(w, c.Value, sess)
			return
		}
	}
	if len(s.sessions) >= 8 {
		var oldest string
		for id, sess := range s.sessions {
			if oldest == "" || sess.Expires.Before(s.sessions[oldest].Expires) {
				oldest = id
			}
		}
		delete(s.sessions, oldest)
	}
	id := random()
	csrf := random()
	s.sessionReply(w, id, session{CSRF: csrf, HardExpires: s.now().Add(sessionMaxLifetime)})
}

// Caller holds mu. Renewal never extends the original absolute lifetime.
func (s *Server) touchSession(id string, sess session) session {
	now := s.now()
	sess.Expires = now.Add(sessionIdleLifetime)
	if sess.Expires.After(sess.HardExpires) {
		sess.Expires = sess.HardExpires
	}
	s.sessions[id] = sess
	return sess
}
func (s *Server) sessionReply(w http.ResponseWriter, id string, sess session) {
	sess = s.touchSession(id, sess)
	// Keep the browser credential until the absolute limit; idle expiry is
	// enforced server-side. Business responses must not set cookies: a slow
	// response can arrive after a new launch and overwrite its fresh credential.
	maxAge := int((sess.HardExpires.Sub(s.now()) + time.Second - 1) / time.Second)
	http.SetCookie(w, &http.Cookie{Name: s.cookieName, Value: id, Path: "/onboard/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: maxAge})
	write(w, 200, map[string]any{"csrf": sess.CSRF, "expires_at": sess.Expires, "max_expires_at": sess.HardExpires})
}
