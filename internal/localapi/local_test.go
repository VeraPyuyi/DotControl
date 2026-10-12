package localapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestClientErrorKeepsStatusWithoutResponseContent(t *testing.T) {
	for _, status := range []int{401, 404, 500} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "private response content", status)
		}))
		c := Client{Address: strings.TrimPrefix(s.URL, "http://"), Token: strings.Repeat("t", 32)}
		data, err := c.Post(context.Background(), "/admin/shutdown", nil)
		s.Close()
		var response *ResponseError
		if !errors.As(err, &response) || response.StatusCode != status || err.Error() != "agent_request_refused" || len(data) != 0 {
			t.Fatalf("unsafe or unclassified response: status=%d err=%v", status, err)
		}
	}
}

func TestGuardRequiresTokenLoopbackHostAndNoOrigin(t *testing.T) {
	token := strings.Repeat("a", 64)
	for _, tc := range []struct {
		name, host, token, origin string
		status                    int
	}{
		{"valid IPv4", "127.0.0.1:9317", token, "", 200}, {"valid IPv6", "[::1]:9317", token, "", 200},
		{"missing token", "127.0.0.1:9317", "", "", 401}, {"wrong token", "127.0.0.1:9317", strings.Repeat("b", 64), "", 401},
		{"DNS rebinding hostname", "attacker.example:9317", token, "", 403}, {"localhost DNS", "localhost:9317", token, "", 403}, {"nonloopback IP", "100.64.0.1:9317", token, "", 403},
		{"browser origin", "127.0.0.1:9317", token, "https://attacker.example", 403}, {"same origin browser", "127.0.0.1:9317", token, "http://127.0.0.1:9317", 403}, {"opaque origin", "127.0.0.1:9317", token, "null", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			h := Guard(token, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(http.StatusOK) }))
			r := httptest.NewRequest("POST", "http://127.0.0.1/rpc", nil)
			r.Host = tc.host
			r.Header.Set("X-DotControl-Token", tc.token)
			r.Header.Set("Origin", tc.origin)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status=%d want=%d", w.Code, tc.status)
			}
			if called != (tc.status == 200) {
				t.Fatal("incorrect handler admission")
			}
			if w.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("CORS enabled")
			}
			if called && w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("sensitive response can be cached")
			}
		})
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "http://127.0.0.1/rpc", nil)
	r.Header.Set("X-DotControl-Token", "short")
	Guard("short", http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("weak token admitted") })).ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("weak configured token status=%d", w.Code)
	}
}

func TestLoopbackListenerAndClientRestrictions(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:0", ":0", "localhost:0", "192.168.1.1:0", "[::]:0"} {
		if l, err := Listen(addr); err == nil {
			l.Close()
			t.Errorf("unsafe listener %q accepted", addr)
		}
		if _, err := (Client{Address: addr}).Post(context.Background(), "/rpc", nil); err == nil {
			t.Errorf("unsafe client %q accepted", addr)
		}
	}
	l, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
}

func TestLocalClientAuthenticatedRoundTripAndRedirectRefusal(t *testing.T) {
	token := strings.Repeat("c", 64)
	var calls atomic.Int32
	s := httptest.NewServer(Guard(token, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "POST" || r.URL.Path != "/rpc" {
			t.Error("wrong local request")
		}
		_, _ = io.WriteString(w, `{"value":{"device_id":"device-a"}}`)
	})))
	defer s.Close()
	c := Client{Address: strings.TrimPrefix(s.URL, "http://"), Token: token}
	data, err := c.Post(context.Background(), "/rpc", map[string]any{"method": "devices"})
	if err != nil || !strings.Contains(string(data), "device-a") {
		t.Fatalf("roundtrip=%s err=%v", data, err)
	}
	c.Token = "wrong"
	if _, err = c.Post(context.Background(), "/rpc", nil); err == nil {
		t.Fatal("incorrect token accepted")
	}
	if calls.Load() != 1 {
		t.Fatal("unauthenticated call reached handler")
	}
	var followed atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed.Add(1) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	c.Address = strings.TrimPrefix(redirect.URL, "http://")
	c.Token = token
	if _, err = c.Post(context.Background(), "/rpc", nil); err == nil {
		t.Fatal("redirect accepted")
	}
	if followed.Load() != 0 {
		t.Fatal("token-bearing request followed redirect")
	}
}
