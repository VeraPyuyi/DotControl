package main

import (
	"encoding/json"
	"strings"
	"testing"
)

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
