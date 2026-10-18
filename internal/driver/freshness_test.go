package driver

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDiagnosticFreshness(t *testing.T) {
	d := &Cua{}
	d.recordSuccess()
	got := d.Diagnostic()
	if got["sample_time"] == nil || got["expires_at"] == nil || got["connected"] != true {
		t.Fatal(got)
	}
	d.diagnostic.sampleTime = time.Now().Add(-2 * time.Minute)
	got = d.Diagnostic()
	if got["connected"] != false || got["stale_reason"] != "sample_expired" {
		t.Fatal(got)
	}
	b, _ := json.Marshal(got)
	if strings.Contains(string(b), "password") {
		t.Fatal(string(b))
	}
}
