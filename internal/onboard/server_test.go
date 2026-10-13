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

func (f *connectionFake) History(context.Context, HistoryRequest) (any, error) {
	f.calls++
	return map[string]bool{"local": true}, nil
}
func TestHistoryPolicyRequiresLocalSession(t *testing.T) {
	f := &connectionFake{}
	s := New("127.0.0.1:9317", f)
	origin := "http://127.0.0.1:9317"
	if req(s, "/onboard/api/history", `{"operation":"apply"}`, origin, "", nil).Code != 401 {
		t.Fatal("unprotected history")
	}
	ticket := strings.Split(s.LaunchURL(), "#")[1]
	w := req(s, "/onboard/api/session", `{"ticket":"`+ticket+`"}`, origin, "", nil)
	var session map[string]string
	json.Unmarshal(w.Body.Bytes(), &session)
	cookie := w.Result().Cookies()[0]
	if req(s, "/onboard/api/history", `{"operation":"apply"}`, origin, "wrong", cookie).Code != 403 {
		t.Fatal("csrf missing")
	}
	if req(s, "/onboard/api/history", `{"operation":"apply","password":"never"}`, origin, session["csrf"], cookie).Code != 400 {
		t.Fatal("unknown fields accepted")
	}
	if req(s, "/onboard/api/history", `{"operation":"status"}`, origin, session["csrf"], cookie).Code != 200 || f.calls != 1 {
		t.Fatal("local history unreachable")
	}
}

type connectionFake struct {
	fake
	calls int
}

func TestLocalReviewEndpointRemoved(t *testing.T) {
	f := &connectionFake{}
	s := New("127.0.0.1:9317", f)
	origin := "http://127.0.0.1:9317"
	ticket := strings.Split(s.LaunchURL(), "#")[1]
	w := req(s, "/onboard/api/session", `{"ticket":"`+ticket+`"}`, origin, "", nil)
	var session map[string]string
	json.Unmarshal(w.Body.Bytes(), &session)
	cookie := w.Result().Cookies()[0]
	for _, op := range []string{"list", "prompt", "answer", "take_over"} {
		w = req(s, "/onboard/api/reviews", `{"operation":"`+op+`"}`, origin, session["csrf"], cookie)
		if w.Code != 404 || f.calls != 0 {
			t.Fatalf("retired endpoint: %d calls %d", w.Code, f.calls)
		}
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

func TestConnectionConfigurationRejectsPresentRetiredFieldsBeforeBackend(t *testing.T) {
	f := &connectionFake{}
	s := New("127.0.0.1:9317", f)
	origin := "http://127.0.0.1:9317"
	ticket := strings.Split(s.LaunchURL(), "#")[1]
	w := req(s, "/onboard/api/session", `{"ticket":"`+ticket+`"}`, origin, "", nil)
	var session map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &session)
	cookie := w.Result().Cookies()[0]
	for _, operation := range []string{"enable_terminal", "save_execution", "save_codex", "list"} {
		for _, field := range []string{"server", "Server", "server_profile_id", "SERVER_PROFILE_ID", "public_key_path", "Public_Key_Path", "authorization_id", "AUTHORIZATION_ID"} {
			values := []string{`null`, `""`}
			if strings.EqualFold(field, "server") {
				values = []string{`null`, `{}`}
			}
			for _, value := range values {
				body := `{"operation":"` + operation + `","` + field + `":` + value + `}`
				w = req(s, "/onboard/api/connections", body, origin, session["csrf"], cookie)
				var response map[string]string
				_ = json.Unmarshal(w.Body.Bytes(), &response)
				if w.Code != 400 || response["error"] != "feature_retired" || f.calls != 0 {
					t.Fatalf("retired request %s: %d %s calls=%d", body, w.Code, w.Body.String(), f.calls)
				}
			}
		}
	}
	for _, body := range []string{
		`{"operation":"save_execution","unknown":null}`,
		`{"operation":"save_execution","experiment_id":null}`,
		`{"operation":"save_execution","execution":{"unknown":null}}`,
		`{"operation":"list"} {}`,
	} {
		w = req(s, "/onboard/api/connections", body, origin, session["csrf"], cookie)
		if w.Code != 400 || !strings.Contains(w.Body.String(), "invalid_input") || f.calls != 0 {
			t.Fatalf("strict request decode lost: %s %d %s", body, w.Code, w.Body.String())
		}
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
