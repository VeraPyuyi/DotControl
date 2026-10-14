package onboard

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type networkFake struct {
	fake
	calls int
	last  NetworkRequest
}

func (f *networkFake) Network(_ context.Context, r NetworkRequest) (any, error) {
	f.calls++
	f.last = r
	return map[string]bool{"saved": true}, nil
}
func TestNetworkEndpointAcceptsOnlyLocalTypedSettings(t *testing.T) {
	f := &networkFake{}
	s := New("127.0.0.1:9317", f)
	origin := "http://127.0.0.1:9317"
	ticket := strings.Split(s.LaunchURL(), "#")[1]
	w := req(s, "/onboard/api/session", `{"ticket":"`+ticket+`"}`, origin, "", nil)
	var session map[string]string
	json.Unmarshal(w.Body.Bytes(), &session)
	cookie := w.Result().Cookies()[0]
	if req(s, "/onboard/api/network", `{"mode":"auto","password":"never"}`, origin, session["csrf"], cookie).Code != 400 || f.calls != 0 {
		t.Fatal("secret field accepted")
	}
	if req(s, "/onboard/api/network", `{"mode":"auto"}`, "https://other.invalid", session["csrf"], cookie).Code != 403 || f.calls != 0 {
		t.Fatal("cross-origin mutation accepted")
	}
	w = req(s, "/onboard/api/network", `{"mode":"manual","proxy_url":"http://127.0.0.1:8118"}`, origin, session["csrf"], cookie)
	if w.Code != 200 || f.calls != 1 || f.last.Mode != "manual" {
		t.Fatalf("network setting not dispatched: %d", w.Code)
	}
}

func TestNetworkEndpointRequiresProtectedSession(t *testing.T) {
	s := New("127.0.0.1:9317", &fake{})
	origin := "http://127.0.0.1:9317"
	if req(s, "/onboard/api/network", `{"mode":"auto"}`, origin, "", nil).Code != 401 {
		t.Fatal("network settings lack authentication")
	}
	ticket := strings.Split(s.LaunchURL(), "#")[1]
	w := req(s, "/onboard/api/session", `{"ticket":"`+ticket+`"}`, origin, "", nil)
	var session map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	cookie := w.Result().Cookies()[0]
	if req(s, "/onboard/api/network", `{"mode":"auto"}`, origin, "wrong", cookie).Code != 403 {
		t.Fatal("network settings lack CSRF")
	}
	w = req(s, "/onboard/api/network", `{"mode":"auto"}`, origin, session["csrf"], cookie)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "network_unavailable") {
		t.Fatalf("missing typed local network endpoint: %d %s", w.Code, w.Body.String())
	}
}
