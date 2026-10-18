package tunnel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func healthFixture(now time.Time) map[string]any {
	return map[string]any{"schema_version": 1, "live": true, "ready": true, "snapshot_at": now, "runtime": map[string]any{"instance_id": "current", "started_at": now.Add(-time.Minute)}, "components": map[string]any{
		"control-plane":     map[string]any{"status": "ok", "state": "polling", "details": map[string]any{"last_success": now.Add(-10 * time.Second), "last_attempt": now.Add(-5 * time.Second), "consecutive_failures": 0, "deadline_seconds": 35, "current_poll_age_seconds": 5}},
		"dispatcher":        map[string]any{"status": "ok", "details": map[string]any{"active": 0}},
		"response-delivery": map[string]any{"status": "unknown", "details": map[string]any{"in_progress": 0}},
		"queue":             map[string]any{"status": "ok", "details": map[string]any{"depth": 0}},
	}}
}
func component(f map[string]any, name string) map[string]any {
	return f["components"].(map[string]any)[name].(map[string]any)
}
func details(f map[string]any, name string) map[string]any {
	return component(f, name)["details"].(map[string]any)
}

func TestHealthRequiresCurrentSuccessfulCloudEvidence(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
		state  string
		ready  bool
	}{
		{"long poll healthy", func(map[string]any) {}, "online", true},
		{"local ready without cloud success", func(f map[string]any) {
			delete(details(f, "control-plane"), "last_success")
			component(f, "control-plane")["status"] = "unknown"
		}, "starting", false},
		{"latest network failure", func(f map[string]any) {
			d := details(f, "control-plane")
			d["last_error"] = now.Add(-time.Second)
			d["consecutive_failures"] = 1
			d["failure_category"] = "dns"
			component(f, "control-plane")["status"] = "degraded"
		}, "degraded", false},
		{"stale success", func(f map[string]any) {
			f["runtime"].(map[string]any)["started_at"] = now.Add(-10 * time.Minute)
			details(f, "control-plane")["last_success"] = now.Add(-91 * time.Second)
		}, "unconfirmed", false},
		{"long deadline remains fresh", func(f map[string]any) {
			f["runtime"].(map[string]any)["started_at"] = now.Add(-10 * time.Minute)
			details(f, "control-plane")["last_success"] = now.Add(-100 * time.Second)
			details(f, "control-plane")["deadline_seconds"] = 60
		}, "online", true},
		{"backpressure distinguished", func(f map[string]any) { component(f, "control-plane")["state"] = "backpressured" }, "backpressured", true},
		{"dead runtime", func(f map[string]any) { f["live"] = false }, "unavailable", false},
		{"missing details", func(f map[string]any) { delete(component(f, "control-plane"), "details") }, "unavailable", false},
		{"unsupported schema", func(f map[string]any) { f["schema_version"] = 2 }, "unavailable", false},
		{"missing runtime", func(f map[string]any) { delete(f, "runtime") }, "unavailable", false},
		{"stale snapshot", func(f map[string]any) { f["snapshot_at"] = now.Add(-time.Minute) }, "unavailable", false},
		{"future success", func(f map[string]any) { details(f, "control-plane")["last_success"] = now.Add(time.Minute) }, "unavailable", false},
		{"local not ready", func(f map[string]any) { f["ready"] = false }, "online", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := healthFixture(now)
			tc.change(f)
			b, _ := json.Marshal(f)
			g := healthGeneration{launchedAt: now.Add(-11 * time.Minute)}
			got := g.observe(b, now)
			if got.Ready != tc.ready || got.CloudState != tc.state {
				t.Fatalf("got %+v want ready=%v state=%s", got, tc.ready, tc.state)
			}
		})
	}
}

func TestHealthCredentialFailureCodes(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		status int
		code   string
	}{
		{401, "tunnel_credentials_rejected"},
		{403, "tunnel_permission_denied"},
		{407, "requires_local_proxy_auth"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			fixture := healthFixture(now)
			component(fixture, "control-plane")["status"] = "degraded"
			poll := details(fixture, "control-plane")
			poll["consecutive_failures"] = 1
			poll["http_status"] = tc.status
			poll["failure_category"] = "http_error"
			raw, err := json.Marshal(fixture)
			if err != nil {
				t.Fatal(err)
			}
			generation := healthGeneration{launchedAt: now.Add(-time.Minute)}
			got := generation.observe(raw, now)
			if got.Ready || got.CloudState != "degraded" || got.Error != tc.code {
				t.Fatalf("credential failure misreported: %+v", got)
			}
		})
	}
}

func TestOfficialHTTPErrorCategoryIsRecognized(t *testing.T) {
	if got := pollError("http_error", "", 502); got != "tunnel_http_failed" {
		t.Fatalf("http_error classified as %s", got)
	}
	if got := pollError("network_error", "", 0); got != "tunnel_network_failed" {
		t.Fatalf("network_error classified as %s", got)
	}
}

func TestHealthGenerationCannotReuseAnotherProcess(t *testing.T) {
	now := time.Now().UTC()
	f := healthFixture(now)
	g := healthGeneration{launchedAt: now.Add(-time.Minute)}
	b, _ := json.Marshal(f)
	if !g.observe(b, now).Ready {
		t.Fatal("current generation should be ready")
	}
	f["runtime"].(map[string]any)["instance_id"] = "different"
	b, _ = json.Marshal(f)
	got := g.observe(b, now)
	if got.Ready || got.Error != "tunnel_health_generation_mismatch" {
		t.Fatal(got)
	}
	g = healthGeneration{launchedAt: now}
	b, _ = json.Marshal(healthFixture(now))
	if g.observe(b, now).Ready {
		t.Fatal("old process snapshot accepted")
	}
}

func TestHealthFetchIsLoopbackDirectBoundedAndUsesDetails(t *testing.T) {
	now := time.Now().UTC()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RequestURI() != "/health?details=true" {
			t.Errorf("wrong endpoint %s", r.URL.RequestURI())
		}
		json.NewEncoder(w).Encode(healthFixture(now))
	}))
	defer server.Close()
	t.Setenv("HTTP_PROXY", "http://user:secret@127.0.0.1:1")
	g := healthGeneration{launchedAt: now.Add(-time.Minute)}
	got := g.fetch(context.Background(), server.URL, now)
	if !got.Ready {
		t.Fatal(got)
	}
	for _, bad := range []string{"http://127.0.0.1.evil.test:1234", "http://127.0.0.1:1@evil.test", "https://127.0.0.1:1", "http://example.test", "http://127.0.0.1:1234/path"} {
		if got := g.fetch(context.Background(), bad, now); got.Ready || got.Error != "tunnel_health_url_invalid" {
			t.Fatal(got)
		}
	}
	huge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(strings.Repeat(" ", 65537))) }))
	defer huge.Close()
	if g.fetch(context.Background(), huge.URL, now).Ready {
		t.Fatal("oversized accepted")
	}
}
