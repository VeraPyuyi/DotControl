package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dotcontrol/dotcontrol/internal/config"
	"github.com/dotcontrol/dotcontrol/internal/dotbridge"
)

func TestBridgeOperatorBootstrap(t *testing.T) {
	dir := t.TempDir()
	original := config.New("operator fixture", "fixture-driver")
	if err := config.Save(dir, original); err != nil {
		t.Fatal(err)
	}
	if err := bridgeCLI(context.Background(), dir, original, &unavailableBridgeVault{}, []string{"enable"}); err != nil {
		t.Fatal(err)
	}
	got, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	original.DotBridgeEnabled = true
	if !reflect.DeepEqual(got, original) {
		t.Fatalf("configuration changed beyond bridge flag: %+v", got)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "config.json" {
		t.Fatal("bootstrap created runtime state")
	}
}

func TestBridgeOperatorProbeModes(t *testing.T) {
	var received map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/bridge/probe" || r.Header.Get("X-DotControl-Token") != "fixture-admin" {
			t.Error("unexpected admin request")
		}
		_ = json.NewDecoder(r.Body).Decode(&received)
		_, _ = w.Write([]byte(`{"job_id":"fixture"}`))
	}))
	defer srv.Close()
	v := dotbridge.NewFakeVault()
	_ = v.Set("admin-token", "fixture-admin")
	c := config.Config{Listen: strings.TrimPrefix(srv.URL, "http://")}
	for _, tc := range []struct {
		args []string
		mode string
	}{
		{[]string{"probe", "client.fixture"}, "answer"},
		{[]string{"probe", "client.fixture", "--mode", "answer"}, "answer"},
		{[]string{"probe", "client.fixture", "--mode", "task"}, "task"},
	} {
		if err := bridgeCLI(context.Background(), t.TempDir(), c, v, tc.args); err != nil {
			t.Fatal(err)
		}
		if received["mode"] != tc.mode || received["client_id"] != "client.fixture" {
			t.Fatalf("payload = %v", received)
		}
	}
	for _, args := range [][]string{{"probe", "client", "--mode"}, {"probe", "client", "--mode", "bad"}, {"probe", "client", "--unknown"}, {"probe", "client", "extra"}, {"probe", "client", "--mode", "task", "extra"}} {
		if err := bridgeCLI(context.Background(), t.TempDir(), c, &unavailableBridgeVault{}, args); err == nil || err.Error() == "local_credential_missing" {
			t.Fatalf("args %v: %v", args, err)
		}
	}
}

func TestBridgeOperatorRejectAddressBeforeCredential(t *testing.T) {
	for _, address := range []string{"localhost:9317", "example.com:9317", "192.0.2.1:9317", "127.0.0.1:0"} {
		err := bridgeClientGet(context.Background(), t.TempDir(), address, "client.fixture", "/dot-bridge/v1/status")
		if err == nil || err.Error() != "invalid_local_address" {
			t.Fatalf("%s: %v", address, err)
		}
		err = bridgePost(context.Background(), config.Config{Listen: address}, &unavailableBridgeVault{}, "/admin/bridge/status", nil)
		if err == nil || err.Error() != "invalid_local_address" {
			t.Fatalf("admin %s: %v", address, err)
		}
	}
}

func TestBridgeOperatorPairRedactsToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"pairing_file":"C:\\fixture\\pairing-client.fixture.json","client_id":"client.fixture","queue_id":"client.fixture","token":"secret-token"}`))
	}))
	defer srv.Close()
	v := dotbridge.NewFakeVault()
	_ = v.Set("admin-token", "fixture-admin")
	c := config.Config{Listen: strings.TrimPrefix(srv.URL, "http://")}
	var printed strings.Builder
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	err := bridgeCLI(context.Background(), t.TempDir(), c, v, []string{"pair"})
	_ = w.Close()
	os.Stdout = old
	_, _ = io.Copy(&printed, r)
	if err != nil {
		t.Fatal(err)
	}
	out := printed.String()
	if strings.Contains(out, "secret-token") {
		t.Fatalf("token leaked: %s", out)
	}
	if !strings.Contains(out, "pairing-client.fixture.json") || !strings.Contains(out, "client.fixture") {
		t.Fatalf("metadata missing: %s", out)
	}
}

func TestBridgeOperatorJobsRequestPath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.Method != http.MethodGet {
			t.Fatalf("method %s", r.Method)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer fixture-pairing" {
			t.Fatalf("auth %q", auth)
		}
		_, _ = w.Write([]byte(`{"version":1,"jobs":[]}`))
	}))
	defer srv.Close()
	dir := physicalTemp(t)
	bridgeRoot := filepath.Join(dir, "dot-bridge")
	pairingPath := filepath.Join(bridgeRoot, "pairing-client.fixture.json")
	if err := os.MkdirAll(bridgeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pairingPath, []byte(`{"version":1,"client_id":"client.fixture","queue_id":"client.fixture","token":"fixture-pairing","listen":"127.0.0.1:9317"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := bridgeCLI(context.Background(), dir, config.Config{Listen: strings.TrimPrefix(srv.URL, "http://")}, &unavailableBridgeVault{}, []string{"jobs", "client.fixture"}); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/dot-bridge/v1/jobs" {
		t.Fatalf("path %q", gotPath)
	}
}

func TestBridgeOperatorPairTraversal(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "pairing-target.json"), []byte(`{"token":"secret"}`), 0600)
	for _, id := range []string{"../target", `..\target`, "x/y", "x:y", ".", ""} {
		if _, err := loadPairingToken(dir, id); err == nil {
			t.Fatalf("accepted %q", id)
		}
	}
}
