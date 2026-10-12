package onboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fake struct{ connected int }

type connectionFake struct {
	fake
	calls int
}

func (f *connectionFake) Reviews(context.Context, ReviewRequest) (any, error) {
	f.calls++
	return map[string]bool{"local": true}, nil
}

func TestLocalReviewRequiresProtectedSessionAndRejectsCredentialFields(t *testing.T) {
	f := &connectionFake{}
	s := New("127.0.0.1:9317", f)
	origin := "http://127.0.0.1:9317"
	if req(s, "/onboard/api/reviews", `{"operation":"list"}`, origin, "", nil).Code != 401 {
		t.Fatal("unprotected approval")
	}
	ticket := strings.Split(s.LaunchURL(), "#")[1]
	w := req(s, "/onboard/api/session", `{"ticket":"`+ticket+`"}`, origin, "", nil)
	var session map[string]string
	json.Unmarshal(w.Body.Bytes(), &session)
	cookie := w.Result().Cookies()[0]
	if req(s, "/onboard/api/reviews", `{"operation":"list"}`, origin, session["csrf"], cookie).Code != 200 {
		t.Fatal("missing local review")
	}
	if req(s, "/onboard/api/reviews", `{"operation":"answer","password":"never"}`, origin, session["csrf"], cookie).Code != 400 {
		t.Fatal("credential field accepted")
	}
	if req(s, "/onboard/api/reviews", `{"operation":"answer"}`, origin, "wrong", cookie).Code != 403 {
		t.Fatal("missing csrf")
	}
}

func (f *connectionFake) Connections(context.Context, ConnectionsRequest) (any, error) {
	f.calls++
	return map[string]bool{"saved": true}, nil
}

func TestConnectionConfigurationRequiresLocalSessionAndRejectsSecrets(t *testing.T) {
	f := &connectionFake{}
	s := New("127.0.0.1:9317", f)
	origin := "http://127.0.0.1:9317"
	if req(s, "/onboard/api/connections", `{"operation":"list"}`, origin, "", nil).Code != 401 {
		t.Fatal("unauthenticated configuration")
	}
	ticket := strings.Split(s.LaunchURL(), "#")[1]
	w := req(s, "/onboard/api/session", `{"ticket":"`+ticket+`"}`, origin, "", nil)
	var session map[string]string
	json.Unmarshal(w.Body.Bytes(), &session)
	cookie := w.Result().Cookies()[0]
	for _, body := range []string{`{"operation":"save_server","password":"private"}`, `{"operation":"save_server","server":{"password":"private"}}`} {
		if req(s, "/onboard/api/connections", body, origin, session["csrf"], cookie).Code != 400 {
			t.Fatal("secret field accepted")
		}
	}
	if f.calls != 0 {
		t.Fatal("invalid request reached storage")
	}
	if req(s, "/onboard/api/connections", `{"operation":"list"}`, origin, session["csrf"], cookie).Code != 200 || f.calls != 1 {
		t.Fatal("valid local request failed")
	}
}

func (f *fake) Status(context.Context) (any, error)         { return map[string]bool{"paused": true}, nil }
func (f *fake) Connect(context.Context, Connect) error      { f.connected++; return nil }
func (f *fake) Action(context.Context, string) (any, error) { return nil, nil }
func (f *fake) Diagnostics(context.Context) (any, error)    { return nil, nil }
func req(s *Server, path, body, origin, csrf string, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "http://127.0.0.1:9317"+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	r.Header.Set("X-DotControl-CSRF", csrf)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}
func TestSessionIsSingleUseSameOriginAndCSRFProtected(t *testing.T) {
	f := &fake{}
	s := New("127.0.0.1:9317", f)
	ticket := strings.Split(s.LaunchURL(), "#")[1]
	body := `{"ticket":"` + ticket + `"}`
	origin := "http://127.0.0.1:9317"
	for _, bad := range []string{"", "http://evil.test", "http://localhost:9317"} {
		if req(s, "/onboard/api/session", body, bad, "", nil).Code != 403 {
			t.Fatal("origin accepted", bad)
		}
	}
	w := req(s, "/onboard/api/session", body, origin, "", nil)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	var v map[string]string
	json.Unmarshal(w.Body.Bytes(), &v)
	cookie := w.Result().Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("insecure cookie")
	}
	if req(s, "/onboard/api/session", body, origin, "", nil).Code != 401 {
		t.Fatal("ticket reused")
	}
	data := `{"name":"test","tunnel_id":"tunnel_abc","runtime_key":"secret"}`
	if req(s, "/onboard/api/connect", data, origin, "wrong", cookie).Code != 403 {
		t.Fatal("csrf accepted")
	}
	if req(s, "/onboard/api/connect", data, origin, v["csrf"], cookie).Code != 200 || f.connected != 1 {
		t.Fatal("valid request failed")
	}
	if req(s, "/onboard/api/connect", data+`{}`, origin, v["csrf"], cookie).Code != 400 || f.connected != 1 {
		t.Fatal("trailing JSON accepted")
	}
}
func TestHostRebindingAndUntrustedErrorRedaction(t *testing.T) {
	s := New("127.0.0.1:9317", &fake{})
	r := httptest.NewRequest("GET", "http://evil.test/onboard/", nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("rebound host accepted")
	}
}
