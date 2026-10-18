package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/dotcontrol/dotcontrol/internal/tunnel"
)

func TestDiagnosticTunnelStatusFixtures(t *testing.T) {
	data, err := os.ReadFile("testdata/diagnostic_tunnel_errors.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Status tunnel.Status `json:"status"`
		Layer  string        `json:"layer"`
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures) == 0 {
		t.Fatal("no tunnel status fixtures")
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Status.Error, func(t *testing.T) {
			if got := diagnosticFailureLayer(fixture.Status.Error); got != fixture.Layer {
				t.Fatalf("actual tunnel status %s classified as %s, want %s", fixture.Status.Error, got, fixture.Layer)
			}
		})
	}
}

func TestDiagnosticFailureLayers(t *testing.T) {
	for _, tc := range []struct{ code, layer string }{{"tunnel_dns_failed", "network"}, {"http_401", "credential"}, {"connection_closed", "driver"}, {"session_ended", "session"}, {"source_dot_unconfirmed", "source_dot"}, {"secret=abc", "unknown"}} {
		got := diagnosticFailureLayer(tc.code)
		if got != tc.layer {
			t.Fatalf("%s %s", tc.code, got)
		}
		b, _ := json.Marshal(got)
		if strings.Contains(string(b), "secret") {
			t.Fatal(string(b))
		}
	}
}
