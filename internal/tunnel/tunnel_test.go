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
	if seen["HTTPS_PROXY"] != "http://127.0.0.1:8888" {
		t.Fatal("required proxy configuration missing")
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
