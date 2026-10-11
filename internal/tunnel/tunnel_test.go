package tunnel

import (
	"strings"
	"testing"
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
