// Package onboard serves a loopback-only, short-lived authenticated local UI.
// The MCP/admin token endpoints keep their existing stricter guards.
package onboard

import (
	"context"
	"crypto/rand"
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
	CSRF    string
	Expires time.Time
}
type Server struct {
	origin   string
	backend  Backend
	assets   fs.FS
	mu       sync.Mutex
	tickets  map[string]time.Time
	sessions map[string]session
}

func New(address string, b Backend) *Server {
	assets, _ := fs.Sub(files, "web")
	return NewWithAssets(address, b, assets)
}

func NewWithAssets(address string, b Backend, assets fs.FS) *Server {
	return &Server{origin: "http://" + address, backend: b, assets: assets, tickets: map[string]time.Time{}, sessions: map[string]session{}}
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
	s.tickets[t] = time.Now().Add(2 * time.Minute)
	return s.origin + "/onboard/#" + t
}
func (s *Server) prune() {
	for k, v := range s.tickets {
		if time.Now().After(v) {
			delete(s.tickets, k)
		}
	}
	for k, v := range s.sessions {
		if time.Now().After(v.Expires) {
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
	if r.URL.Path == "/onboard/" || r.URL.Path == "/onboard/app.js" || r.URL.Path == "/onboard/connections.js" || r.URL.Path == "/onboard/reviews.js" || r.URL.Path == "/onboard/style.css" {
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
		case "app.js", "connections.js", "reviews.js":
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
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if r.URL.Path == "/onboard/api/session" {
		s.bootstrap(w, r)
		return
	}
	c, err := r.Cookie("dotcontrol_session")
	if err != nil {
		http.Error(w, "open_from_dotcontrol", 401)
		return
	}
	s.mu.Lock()
	s.prune()
	sess, ok := s.sessions[c.Value]
	s.mu.Unlock()
	if !ok || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-DotControl-CSRF")), []byte(sess.CSRF)) != 1 {
		http.Error(w, "session_rejected", 403)
		return
	}
	var value any
	switch r.URL.Path {
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
		if c, e := r.Cookie("dotcontrol_session"); e == nil {
			if sess, ok := s.sessions[c.Value]; ok {
				write(w, 200, map[string]string{"csrf": sess.CSRF})
				return
			}
		}
	}
	if _, ok := s.tickets[input.Ticket]; !ok {
		http.Error(w, "launch_expired_open_dotcontrol", 401)
		return
	}
	delete(s.tickets, input.Ticket)
	if len(s.sessions) >= 8 {
		s.sessions = map[string]session{}
	}
	id := random()
	csrf := random()
	s.sessions[id] = session{CSRF: csrf, Expires: time.Now().Add(2 * time.Hour)}
	http.SetCookie(w, &http.Cookie{Name: "dotcontrol_session", Value: id, Path: "/onboard/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 7200})
	write(w, 200, map[string]string{"csrf": csrf})
}
