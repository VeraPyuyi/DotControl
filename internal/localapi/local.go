package localapi

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/gateway"
	"github.com/dotcontrol/dotcontrol/internal/service"
)

func Listen(address string) (net.Listener, error) {
	host, _, e := net.SplitHostPort(address)
	if e != nil {
		return nil, e
	}
	ip, e := netip.ParseAddr(host)
	if e != nil || !ip.IsLoopback() {
		return nil, errors.New("local_listener_must_be_loopback")
	}
	return net.Listen("tcp", address)
}
func Guard(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, e := net.SplitHostPort(r.Host)
		if e != nil {
			host = r.Host
		}
		ip, e := netip.ParseAddr(host)
		if e != nil || !ip.IsLoopback() || r.Header.Get("Origin") != "" {
			http.Error(w, "invalid_origin", 403)
			return
		}
		provided := r.Header.Get("X-DotControl-Token")
		if len(token) < 32 || subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
			http.Error(w, "unauthorized", 401)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

type Client struct{ Address, Token string }

// ResponseError retains only the HTTP status, never an agent response body.
type ResponseError struct{ StatusCode int }

func (e *ResponseError) Error() string { return "agent_request_refused" }

func (c Client) Post(ctx context.Context, path string, input any) (json.RawMessage, error) {
	if !strings.HasPrefix(path, "/") {
		return nil, errors.New("invalid_path")
	}
	host, _, e := net.SplitHostPort(c.Address)
	if e != nil {
		return nil, e
	}
	ip, e := netip.ParseAddr(host)
	if e != nil || !ip.IsLoopback() {
		return nil, errors.New("invalid_local_address")
	}
	b, e := json.Marshal(input)
	if e != nil {
		return nil, e
	}
	r, e := http.NewRequestWithContext(ctx, "POST", "http://"+c.Address+path, bytes.NewReader(b))
	if e != nil {
		return nil, e
	}
	r.Header.Set("X-DotControl-Token", c.Token)
	r.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 50 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect_refused") }}
	res, e := client.Do(r)
	if e != nil {
		return nil, errors.New("agent_not_running")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, &ResponseError{StatusCode: res.StatusCode}
	}
	return io.ReadAll(io.LimitReader(res.Body, 24<<20))
}
func (c Client) Call(ctx context.Context, method string, req service.Request) (any, error) {
	data, e := c.Post(ctx, "/rpc", map[string]any{"method": method, "request": req})
	if e != nil {
		return nil, e
	}
	var env gateway.Envelope
	if json.Unmarshal(data, &env) != nil {
		return nil, errors.New("invalid_agent_response")
	}
	if env.Error != "" {
		return nil, errors.New(env.Error)
	}
	var value any
	e = json.Unmarshal(env.Value, &value)
	return value, e
}
