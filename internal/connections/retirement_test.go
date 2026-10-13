package connections

import (
	"os"
	"path/filepath"
	"testing"
)

func TestServerEditingRetiredWithoutRewritingLegacyConfig(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.Update(func(c *Config) error {
		c.ServerProfiles = []ServerProfile{{ID: "legacy", Name: "Legacy", SSHAlias: "old.invalid"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(s.dir, "connections.json"))
	_, err := s.UpsertServer(ServerProfile{ID: "new", Name: "New", SSHAlias: "new.invalid"})
	if err == nil || err.Error() != "feature_retired" {
		t.Fatalf("server edit: %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(s.dir, "connections.json"))
	if string(before) != string(after) {
		t.Fatal("old config changed")
	}
}
