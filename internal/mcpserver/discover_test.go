package mcpserver

import (
	"bytes"
	"testing"
)

func TestDiscoverPatchOnlySuccessfulModernTypedResult(t *testing.T) {
	for _, raw := range []string{
		`{"jsonrpc":"2.0","id":1,"error":{"code":-1,"message":"failed"}}`,
		`{"jsonrpc":"2.0","id":1,"result":{"supportedVersions":["2025-11-25"],"capabilities":{"tools":{}}}}`,
		`{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`,
	} {
		if got := patchDiscoverEvents([]byte(raw)); !bytes.Equal(got, []byte(raw)) {
			t.Fatalf("changed unsuccessful or unrelated result: %s", got)
		}
	}
	raw := `{"jsonrpc":"2.0","id":9007199254740993,"result":{"supportedVersions":["2026-07-28"],"capabilities":{"tools":{}},"futureNumber":9007199254740993}}`
	got := patchDiscoverEvents([]byte(raw))
	if !bytes.Contains(got, []byte(`"events":{}`)) || bytes.Count(got, []byte("9007199254740993")) != 2 {
		t.Fatalf("patched result: %s", got)
	}
}
