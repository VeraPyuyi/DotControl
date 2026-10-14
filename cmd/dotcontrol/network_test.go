package main

import (
	"context"
	"github.com/dotcontrol/dotcontrol/internal/config"
	"github.com/dotcontrol/dotcontrol/internal/onboard"
	"github.com/dotcontrol/dotcontrol/internal/tunnel"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNetworkSettingsPreserveAccessAndQueueWithoutStarting(t *testing.T) {
	dir := t.TempDir()
	c := config.New("existing", "driver")
	c.TunnelID = "tunnel_existing"
	c.TerminalOnly = true
	c.HistoryEnabled = true
	o := &runtimeOwner{dir: dir, cfg: c, sup: &tunnel.Supervisor{}, revoked: true}
	if err := config.Save(dir, c); err != nil {
		t.Fatal(err)
	}
	history := []byte(`{"mode":"allowlist","revision":2,"allowed_thread_ids":["existing"]}`)
	path := filepath.Join(dir, "history_access.json")
	os.WriteFile(path, history, 0600)
	if _, err := o.Network(context.Background(), onboard.NetworkRequest{Mode: "manual", ProxyURL: "http://127.0.0.1:8118"}); err != nil {
		t.Fatal(err)
	}
	after, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	c.Network = config.Network{Mode: "manual", ProxyURL: "http://127.0.0.1:8118"}
	if !reflect.DeepEqual(after, c) || !reflect.DeepEqual(o.cfg, c) {
		t.Fatal("unrelated configuration changed")
	}
	if !o.revoked || o.sup.Status().Running || !o.sup.Status().NetworkChangePending {
		t.Fatal("network settings changed lifecycle/access")
	}
	b, _ := os.ReadFile(path)
	if string(b) != string(history) {
		t.Fatal("history policy changed")
	}
	before, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	for _, req := range []onboard.NetworkRequest{{Mode: "invalid"}, {Mode: "manual", ProxyURL: "http://user:secret@localhost:81"}} {
		if _, err := o.Network(context.Background(), req); err == nil {
			t.Fatal("invalid settings accepted")
		}
		b, _ := os.ReadFile(filepath.Join(dir, "config.json"))
		if string(b) != string(before) {
			t.Fatal("failed validation changed config")
		}
	}
}

func TestDoctorSeparatesConfigurationFromCloudEvidence(t *testing.T) {
	for _, test := range []struct {
		data  string
		ready bool
		state string
	}{
		{`{"tunnel":{"running":true,"ready":true}}`, false, "unconfirmed"},
		{`{"tunnel":{"running":true,"local_ready":true,"ready":false,"cloud_state":"offline","error":"tunnel_network_error"}}`, false, "offline"},
		{`{"tunnel":{"running":true,"local_ready":true,"ready":true,"cloud_state":"online","last_successful_poll_at":"2026-10-13T00:00:00Z"}}`, true, "online"},
	} {
		got := doctorCloudStatus([]byte(test.data))
		if got.Ready != test.ready || got.CloudState != test.state {
			t.Fatalf("%s => %+v", test.data, got)
		}
	}
}
