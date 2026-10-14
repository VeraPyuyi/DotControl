package onboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type delayedStatusBackend struct {
	fake
	entered chan struct{}
	release chan struct{}
}

func (b *delayedStatusBackend) Status(context.Context) (any, error) {
	close(b.entered)
	<-b.release
	return map[string]bool{"ready": true}, nil
}

func TestLateResponseCannotReplaceFreshBrowserSession(t *testing.T) {
	base := time.Now()
	now := base
	b := &delayedStatusBackend{entered: make(chan struct{}), release: make(chan struct{})}
	s := New("127.0.0.1:9317", b)
	s.now = func() time.Time { return now }
	c, csrf := wizardSession(t, s, nil)
	if c.MaxAge != int(sessionMaxLifetime/time.Second) {
		t.Fatal("browser credential must cover the absolute lifetime")
	}
	for _, elapsed := range []time.Duration{11 * time.Hour, 22 * time.Hour} {
		now = base.Add(elapsed)
		if w := req(s, "/onboard/api/action", `{"action":"check"}`, s.origin, csrf, c); w.Code != 200 {
			t.Fatal("keep session active", w.Code)
		}
	}
	now = base.Add(sessionMaxLifetime - time.Second)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- req(s, "/onboard/api/status", `{}`, s.origin, csrf, c) }()
	<-b.entered
	now = base.Add(sessionMaxLifetime)
	fresh, freshCSRF := wizardSession(t, s, c)
	close(b.release)
	late := <-done
	if fresh.Value == c.Value {
		t.Fatal("expired session reused")
	}
	if len(late.Result().Cookies()) != 0 {
		t.Fatal("late response would overwrite the fresh browser session")
	}
	if w := req(s, "/onboard/api/action", `{"action":"check"}`, s.origin, freshCSRF, fresh); w.Code != 200 {
		t.Fatal("fresh session rejected", w.Code)
	}
}

func wizardSession(t *testing.T, s *Server, cookie *http.Cookie) (*http.Cookie, string) {
	t.Helper()
	ticket := strings.Split(s.LaunchURL(), "#")[1]
	w := req(s, "/onboard/api/session", `{"ticket":"`+ticket+`"}`, "http://127.0.0.1:9317", "", cookie)
	if w.Code != 200 {
		t.Fatalf("bootstrap: %d", w.Code)
	}
	var v map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return w.Result().Cookies()[0], v["csrf"].(string)
}

func TestWizardSurvivesLongSetupAndAuthenticatedActivity(t *testing.T) {
	s := New("127.0.0.1:9317", &fake{})
	ticket := strings.Split(s.LaunchURL(), "#")[1]
	if time.Until(s.tickets[ticket]) < 29*time.Minute {
		t.Fatal("queued launch link expires too soon")
	}
	c, csrf := wizardSession(t, s, nil)
	if c.MaxAge < 11*60*60 || time.Until(s.sessions[c.Value].Expires) < 11*time.Hour {
		t.Fatal("wizard lifetime shorter than a work day")
	}
	sess := s.sessions[c.Value]
	sess.Expires = time.Now().Add(time.Minute)
	s.sessions[c.Value] = sess
	if w := req(s, "/onboard/api/status", `{}`, s.origin, csrf, c); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if time.Until(s.sessions[c.Value].Expires) < 11*time.Hour {
		t.Fatal("authenticated activity did not renew session")
	}
	if w := req(s, "/onboard/api/session", `{}`, s.origin, "", c); w.Code != 200 {
		t.Fatal("page reload could not reuse cookie", w.Code)
	}
}

func TestReopeningSameBrowserPreservesOtherTabs(t *testing.T) {
	s := New("127.0.0.1:9317", &fake{})
	c, csrf := wizardSession(t, s, nil)
	for i := 0; i < 12; i++ {
		next, nextCSRF := wizardSession(t, s, c)
		if next.Value != c.Value || nextCSRF != csrf {
			t.Fatal("reopen replaced this browser's existing session")
		}
	}
	if len(s.sessions) != 1 {
		t.Fatal("same browser consumed session slots")
	}
	if w := req(s, "/onboard/api/status", `{}`, s.origin, csrf, c); w.Code != 200 {
		t.Fatal("old tab invalidated", w.Code)
	}
}

func TestSessionCapacityDoesNotLogOutEveryBrowser(t *testing.T) {
	s := New("127.0.0.1:9317", &fake{})
	for i := 0; i < 9; i++ {
		wizardSession(t, s, nil)
	}
	if len(s.sessions) != 8 {
		t.Fatalf("capacity cleared unrelated sessions: %d", len(s.sessions))
	}
}

func TestInvalidOriginAndCSRFDoNotRenewSession(t *testing.T) {
	s := New("127.0.0.1:9317", &fake{})
	c, _ := wizardSession(t, s, nil)
	before := s.sessions[c.Value].Expires
	for _, origin := range []string{s.origin, "http://evil.test"} {
		if w := req(s, "/onboard/api/status", `{}`, origin, "wrong", c); w.Code != 403 {
			t.Fatal(w.Code)
		}
		if !s.sessions[c.Value].Expires.Equal(before) {
			t.Fatal("rejected request renewed access")
		}
	}
}

func TestSessionIdleHardExpiryAndRestart(t *testing.T) {
	base := time.Now()
	now := base
	s := New("127.0.0.1:9317", &fake{})
	s.now = func() time.Time { return now }
	c, csrf := wizardSession(t, s, nil)
	for _, elapsed := range []time.Duration{11 * time.Hour, 22 * time.Hour, 24*time.Hour - time.Second} {
		now = base.Add(elapsed)
		if w := req(s, "/onboard/api/status", `{}`, s.origin, csrf, c); w.Code != 200 {
			t.Fatal("active session ended early", elapsed, w.Code)
		}
		if !s.sessions[c.Value].HardExpires.Equal(base.Add(24 * time.Hour)) {
			t.Fatal("renewal extended absolute limit")
		}
	}
	now = base.Add(24 * time.Hour)
	if w := req(s, "/onboard/api/status", `{}`, s.origin, csrf, c); w.Code != 403 {
		t.Fatal("absolute expiry bypassed", w.Code)
	}
	if w := req(s, "/onboard/api/session", `{}`, s.origin, "", c); w.Code != 401 {
		t.Fatal("expired session restored", w.Code)
	}
	c, csrf = wizardSession(t, s, nil)
	now = now.Add(12 * time.Hour)
	if w := req(s, "/onboard/api/status", `{}`, s.origin, csrf, c); w.Code != 403 {
		t.Fatal("idle expiry bypassed", w.Code)
	}
	fresh := New("127.0.0.1:9317", &fake{})
	if w := req(fresh, "/onboard/api/session", `{}`, fresh.origin, "", c); w.Code != 401 {
		t.Fatal("restart restored in-memory credentials")
	}
}

func TestDifferentAgentPortsDoNotOverwriteBrowserCookie(t *testing.T) {
	s := New("127.0.0.1:9317", &fake{})
	c, _ := wizardSession(t, s, nil)
	other := New("127.0.0.1:9417", &fake{})
	ticket := strings.Split(other.LaunchURL(), "#")[1]
	r := httptest.NewRequest("POST", other.origin+"/onboard/api/session", strings.NewReader(`{"ticket":"`+ticket+`"}`))
	r.Header.Set("Origin", other.origin)
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(c)
	w := httptest.NewRecorder()
	other.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w.Result().Cookies()[0].Name == c.Name {
		t.Fatal("browser would overwrite other port's session")
	}
}
