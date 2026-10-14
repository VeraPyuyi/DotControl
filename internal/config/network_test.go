package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLegacyConfigDefaultsToAutoNetwork(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"device_id":"test","idle_seconds":60}`), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(c)
	var got map[string]any
	json.Unmarshal(b, &got)
	network, ok := got["network"].(map[string]any)
	if !ok || network["mode"] != "auto" {
		t.Fatalf("legacy network mode = %v, want auto", got["network"])
	}
}

func TestSaveRejectsCredentialBearingOrUnsupportedProxy(t *testing.T) {
	for _, fields := range []string{
		`"network":{"mode":"manual","proxy_url":"http://user:secret@example.test:8080"}`,
		`"network":{"mode":"manual","proxy_url":"socks5://localhost:1080"}`,
		`"network":{"mode":"manual","proxy_url":""}`,
		`"network":{"mode":"unexpected"}`,
	} {
		var c Config
		if err := json.Unmarshal([]byte(`{"device_id":"test","idle_seconds":60,`+fields+`}`), &c); err != nil {
			t.Fatal(err)
		}
		if err := Save(t.TempDir(), c); err == nil {
			t.Errorf("unsafe network configuration was persisted")
		}
	}
}
