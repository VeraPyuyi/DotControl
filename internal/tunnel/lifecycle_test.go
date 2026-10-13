package tunnel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/config"
)

// The test executable can stand in for tunnel-client. It serves only synthetic
// loopback health and never polls a cloud service or invokes an MCP tool.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "run" {
		args := map[string]string{}
		for i := 2; i+1 < len(os.Args); i += 2 {
			args[os.Args[i]] = os.Args[i+1]
		}
		dir := args["--control-plane.tunnel-id"]
		go func() {
			for {
				time.Sleep(20 * time.Millisecond)
				b, _ := os.ReadFile(filepath.Join(dir, "state"))
				if string(b) == "exit" {
					os.WriteFile(filepath.Join(dir, "state"), []byte("polling"), 0600)
					os.Exit(0)
				}
			}
		}()
		initial, _ := os.ReadFile(filepath.Join(dir, "state"))
		if string(initial) == "logs" {
			os.Stderr.WriteString("{\"msg\":\"poll failed; backing off\",\"error\":\"proxyconnect tcp http://dns-user:tls-password@tls-proxy.example:8080 connection reset\"}\n")
			os.Stderr.WriteString("{\"msg\":\"network_error\",\"url\":\"http://secret:secret@dns-proxy.example\",\"time\":\"2026-10-13T10:05:20.401403Z\"}\n")
		}
		started := time.Now().UTC()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			state, _ := os.ReadFile(filepath.Join(dir, "state"))
			now := time.Now().UTC()
			f := healthFixture(now)
			f["runtime"] = map[string]any{"instance_id": strconv.Itoa(os.Getpid()), "started_at": started}
			details(f, "control-plane")["last_success"] = now
			details(f, "control-plane")["last_attempt"] = now
			switch string(state) {
			case "idle_late":
				component(f, "control-plane")["state"] = "idle"
			case "logs":
				component(f, "control-plane")["state"] = "backoff"
				component(f, "control-plane")["status"] = "degraded"
				details(f, "control-plane")["consecutive_failures"] = 1
				details(f, "control-plane")["last_error"] = now
				details(f, "control-plane")["failure_category"] = "network_error"
			case "busy":
				component(f, "control-plane")["state"] = "idle"
				details(f, "dispatcher")["active"] = 1
				details(f, "response-delivery")["in_progress"] = 1
				details(f, "queue")["depth"] = 1
			case "backoff":
				component(f, "control-plane")["state"] = "backoff"
				component(f, "control-plane")["status"] = "degraded"
				details(f, "control-plane")["consecutive_failures"] = 1
				details(f, "control-plane")["last_error"] = now
				details(f, "control-plane")["failure_category"] = "network"
			}
			json.NewEncoder(w).Encode(f)
			if string(state) == "idle_late" {
				// Simulate commands already returned by Poll but not yet visible in
				// queue/dispatcher health when Succeeded records the idle state.
				os.WriteFile(filepath.Join(dir, "late-received"), []byte("received"), 0600)
				go func() {
					time.Sleep(time.Second)
					os.WriteFile(filepath.Join(dir, "late-completed"), []byte("completed"), 0600)
				}()
			}
		}))
		if err := os.WriteFile(args["--health.url-file"], []byte(server.URL), 0600); err != nil {
			os.Exit(2)
		}
		log, err := os.OpenFile(filepath.Join(dir, "launches"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			os.Exit(2)
		}
		log.WriteString(strconv.Itoa(os.Getpid()) + "\n")
		log.Close()
		select {}
	}
	os.Exit(m.Run())
}

func TestStderrURLsAndCredentialsCannotInventDNSOrTLSFailures(t *testing.T) {
	dir := t.TempDir()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state"), []byte("logs"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Start(context.Background(), dir, config.Config{Network: config.Network{Mode: "direct"}, TunnelID: dir, TunnelPath: binary, Listen: "127.0.0.1:1"}, testSecrets{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(dir, "launches")); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	if s.Status().Error != "" {
		t.Fatalf("stderr invented diagnostic: %s", s.Status().Error)
	}
	deadline = time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if s.Status().Error == "tunnel_network_failed" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("structured generic failure was not retained", s.Status())
}

func TestLifecycleKeepsLateArrivalsAndAppliesProxyOnlyAfterChildExit(t *testing.T) {
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	dir := t.TempDir()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	writeState := func(state string) {
		if err := os.WriteFile(filepath.Join(dir, "state"), []byte(state), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeState("polling")
	c := config.Config{Network: config.Network{Mode: "direct"}, TunnelID: dir, TunnelPath: binary, Listen: "127.0.0.1:1"}
	s, err := Start(context.Background(), dir, c, testSecrets{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop()
	waitFor := func(description string, predicate func() bool) {
		t.Helper()
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			if predicate() {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal(description, s.Status())
	}
	launches := func() int { b, _ := os.ReadFile(filepath.Join(dir, "launches")); return len(strings.Fields(string(b))) }
	waitFor("helper never became ready", func() bool { return s.Status().Ready })
	second, err := Start(context.Background(), dir, c, testSecrets{})
	if err == nil {
		second.Stop()
		t.Fatal("second lifecycle manager was allowed")
	}
	if err := s.UpdateNetwork(config.Network{Mode: "manual", ProxyURL: "http://127.0.0.1:8888"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5500 * time.Millisecond)
	if launches() != 1 || !s.Status().NetworkChangePending || s.Status().ProxySource != "direct" {
		t.Fatal("polling process was interrupted", s.Status())
	}
	writeState("busy")
	time.Sleep(5500 * time.Millisecond)
	if launches() != 1 || !s.Status().NetworkChangePending || s.Status().ProxySource != "direct" {
		t.Fatal("busy process was interrupted", s.Status())
	}
	writeState("backoff")
	time.Sleep(5500 * time.Millisecond)
	if launches() != 1 || !s.Status().NetworkChangePending || s.Status().ProxySource != "direct" {
		t.Fatal("backoff snapshot interrupted live process", s.Status())
	}
	writeState("idle_late")
	waitFor("late command did not complete", func() bool { _, err := os.Stat(filepath.Join(dir, "late-completed")); return err == nil })
	if launches() != 1 || !s.Status().NetworkChangePending || s.Status().ProxySource != "direct" {
		t.Fatal("idle snapshot interrupted newly arrived command", s.Status())
	}
	writeState("exit")
	waitFor("natural exit did not apply pending proxy", func() bool {
		return launches() == 2 && s.Status().ProxySource == "manual" && !s.Status().NetworkChangePending
	})
}
