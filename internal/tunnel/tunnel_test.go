package tunnel

import (
	"context"
	"errors"
	"github.com/dotcontrol/dotcontrol/internal/config"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestChildEnvironmentIsAllowlistedAndReplacesInheritedSecrets(t *testing.T) {
	t.Setenv("DOTCONTROL_RUNTIME_KEY", "unrelated-parent-runtime-key")
	t.Setenv("DOTCONTROL_TUNNEL_KEY", "old-inherited-key")
	t.Setenv("DOTCONTROL_MCP_TOKEN", "old-inherited-token")
	t.Setenv("UNRELATED_PRIVATE_VALUE", "must-not-reach-child")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:8888")
	values := Environment("test-tunnel-key", "test-mcp-token")
	seen := map[string]string{}
	for _, item := range values {
		key, value, _ := strings.Cut(item, "=")
		key = strings.ToUpper(key)
		if _, duplicate := seen[key]; duplicate {
			t.Fatalf("duplicate environment key: %s", key)
		}
		seen[key] = value
	}
	for _, key := range []string{"DOTCONTROL_RUNTIME_KEY", "UNRELATED_PRIVATE_VALUE"} {
		if _, present := seen[key]; present {
			t.Fatalf("unrelated secret inherited: %s", key)
		}
	}
	if seen["DOTCONTROL_TUNNEL_KEY"] != "test-tunnel-key" || seen["DOTCONTROL_MCP_TOKEN"] != "test-mcp-token" {
		t.Fatal("child credentials were not explicitly replaced")
	}
	if seen["HTTPS_PROXY"] != "" {
		t.Fatal("generic proxy must never reach local MCP transport")
	}
}

func TestFailureSurvivesRestartAndClearsOnlyAfterNewCloudSuccess(t *testing.T) {
	s := &Supervisor{}
	now := time.Now().UTC()
	s.set(true, false, "tunnel_dns_failed")
	s.set(true, false, "")
	if s.Status().Error != "tunnel_dns_failed" {
		t.Fatal("restart erased diagnostic")
	}
	old := now.Add(-time.Minute)
	s.applyHealth(healthObservation{Ready: true, LocalReady: true, CloudState: "online", LastSuccessfulPollAt: &old}, now)
	if s.Status().Ready || s.Status().Error == "" {
		t.Fatal("old success erased new failure")
	}
	fresh := time.Now().Add(time.Second)
	s.applyHealth(healthObservation{Ready: true, LocalReady: true, CloudState: "online", LastSuccessfulPollAt: &fresh}, fresh)
	if !s.Status().Ready || s.Status().Error != "" {
		t.Fatal("real success did not recover", s.Status())
	}
}

func TestNetworkUpdateDefersUntilValidIdleAndKeepsActiveSource(t *testing.T) {
	s := &Supervisor{Running: true, proxySource: "system", network: config.Network{Mode: "auto"}}
	if err := s.UpdateNetwork(config.Network{Mode: "manual", ProxyURL: "http://local.test:8888"}); err != nil {
		t.Fatal(err)
	}
	current := resolvedNetwork{NetworkStatus: NetworkStatus{Mode: "auto", Source: "system"}, proxyURL: "http://old.test:8080"}
	next := resolvedNetwork{NetworkStatus: NetworkStatus{Mode: "manual", Source: "manual"}, proxyURL: "http://local.test:8888"}
	for _, obs := range []healthObservation{{}, {Ready: true, LocalReady: true, CloudState: "online"}, {CloudState: "backpressured"}} {
		if s.switchNetwork(current, next, obs) {
			t.Fatal("unconfirmed/busy request permitted restart")
		}
		if !s.Status().Running || !s.Status().NetworkChangePending || s.Status().ProxySource != "system" {
			t.Fatal(s.Status())
		}
	}
	if !s.switchNetwork(current, next, healthObservation{SafeToSwitch: true, CloudState: "degraded"}) {
		t.Fatal("idle backoff did not permit switch")
	}
	if err := s.UpdateNetwork(config.Network{Mode: "manual", ProxyURL: "http://user:secret@host"}); err == nil {
		t.Fatal("credentialed update accepted")
	}
}

func TestControlPlaneEnvironmentConfinesAuthenticatedEnvironmentProxy(t *testing.T) {
	t.Setenv("DOTCONTROL_CONTROL_PROXY", "http://user:TEST_SECRET@proxy:8080")
	env, args, err := ControlPlaneEnvironment(context.Background(), config.Network{Mode: "auto"}, "key", "token")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(args, " "), "TEST_SECRET") || strings.Join(args, " ") != "--control-plane.http-proxy env:DOTCONTROL_CONTROL_PROXY" {
		t.Fatal("unsafe arguments")
	}
	found := false
	for _, s := range env {
		k, _, _ := strings.Cut(s, "=")
		if strings.EqualFold(k, "HTTPS_PROXY") || strings.EqualFold(k, "HTTP_PROXY") {
			t.Fatal("global proxy survived")
		}
		if k == "DOTCONTROL_CONTROL_PROXY" {
			found = true
		}
	}
	if !found {
		t.Fatal("scoped credential env missing")
	}
}

type recoveringSecrets struct{ calls atomic.Int32 }

func (v *recoveringSecrets) Get(string) (string, error) {
	n := v.calls.Add(1)
	if n == 3 || n == 4 {
		return "", errors.New("keychain temporarily locked")
	}
	return "dummy-not-a-real-key", nil
}
func TestTransientVaultFailureIsRetried(t *testing.T) {
	v := &recoveringSecrets{}
	dir := t.TempDir()
	s, err := Start(context.Background(), dir, config.Config{TunnelID: "tunnel_test", TunnelPath: filepath.Join(dir, "missing")}, v)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	deadline := time.Now().Add(3 * time.Second)
	for v.calls.Load() < 6 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if v.calls.Load() < 6 {
		t.Fatal("supervisor stopped after transient vault failure")
	}
}

type testSecrets struct{}

func (testSecrets) Get(string) (string, error) { return "dummy-not-a-real-key", nil }
func TestMissingBinaryRetriesWithoutLosingSupervisorAndStopsPromptly(t *testing.T) {
	dir := t.TempDir()
	s, e := Start(context.Background(), dir, config.Config{TunnelID: "tunnel_test", TunnelPath: filepath.Join(dir, "missing-executable"), Listen: "127.0.0.1:12345"}, testSecrets{})
	if e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(time.Second)
	for s.Status().Error == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if s.Status().Error != "tunnel_start_failed" || !s.Status().Configured {
		t.Fatal(s.Status())
	}
	began := time.Now()
	s.Stop()
	if time.Since(began) > time.Second {
		t.Fatal("stop blocked in backoff")
	}
	if s.Status().Running {
		t.Fatal("still running")
	}
}
func TestLogClassifierDoesNotExposeCredentialBearingText(t *testing.T) {
	s := &Supervisor{}
	writer := classifiedLog{s}
	writer.Write([]byte("secret=example PRIVATE upstream x509 certificate failure"))
	if s.Status().Error != "tunnel_tls_failed" {
		t.Fatal(s.Status())
	}
}

func TestLogDigitsCannotInventAuthenticationFailure(t *testing.T) {
	s := &Supervisor{}
	classifiedLog{s}.Write([]byte(`{"time":"2026-10-13T10:05:20.401403Z","message":"poll complete","duration_ms":401}`))
	if s.Status().Error != "" {
		t.Fatal("timestamp/duration became auth error", s.Status())
	}
	classifiedLog{s}.Write([]byte(`{"message":"proxy authentication required"}`))
	if s.Status().Error != "requires_local_proxy_auth" {
		t.Fatal("proxy auth failure not classified", s.Status())
	}
}
