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
