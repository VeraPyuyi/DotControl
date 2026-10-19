package dotbridge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPairingOperatorRejectUnsafeIDs(t *testing.T) {
	for _, id := range []string{"../escape", `..\escape`, "x/y", "x:y", ".", "", "x?y"} {
		if _, err := writePairingFile(t.TempDir(), PairingFile{ClientID: id, Token: "fixture"}); err == nil {
			t.Fatalf("accepted %q", id)
		}
	}
}

func TestPairingOperatorRejectHardlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(target, filepath.Join(root, "pairing-client.fixture.json")); err != nil {
		t.Skip(err)
	}
	if _, err := writePairingFile(root, PairingFile{ClientID: "client.fixture", Token: "fixture"}); err == nil {
		t.Fatal("accepted hardlink")
	}
	b, _ := os.ReadFile(target)
	if string(b) != "unchanged" {
		t.Fatal("modified hardlink target")
	}
}

func TestPairingOperatorAtomicRoundTrip(t *testing.T) {
	root := filepath.Join(t.TempDir(), "bridge")
	original := PairingFile{Version: APIVersion, ClientID: "client.fixture", QueueID: "client.fixture", Token: "fixture-secret", Listen: "127.0.0.1:9317"}
	if _, err := writePairingFile(root, original); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 1 {
		t.Fatalf("temporary files left: %v", entries)
	}
}
