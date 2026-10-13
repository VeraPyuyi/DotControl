package codexbridge

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotcontrol/dotcontrol/internal/connections"
)

func TestProfileCatalogExposesReferencesWithoutLocalConfiguration(t *testing.T) {
	dir := t.TempDir()
	store := connections.NewStore(filepath.Join(dir, "connections"))
	_, err := store.UpsertExecution(connections.ExecutionProfile{ID: "project-one", Name: "Example project", ProjectDir: filepath.Join(dir, "private-project"), PermissionProfile: ":read-only", Execution: "terminal", Presentation: "background"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = seedLegacyServer(store, connections.ServerProfile{ID: "server-one", Name: "Example server", SSHAlias: "example.invalid", SSHPort: 2222, RemoteDir: "/private-work", IdentityFile: filepath.Join(dir, "private-key"), DeployCommand: "private-deploy-command"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(Config{DeviceID: "device", StateDir: dir, Runtime: &RuntimeConfig{ConnectionsDir: filepath.Join(dir, "connections")}})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	catalog := b.ProfileCatalog()
	if catalog == nil || len(catalog.Execution) != 1 || len(catalog.Servers) != 0 || catalog.Execution[0].ID != "project-one" {
		t.Fatalf("catalog missing configured references: %+v", catalog)
	}
	raw, _ := json.Marshal(catalog)
	for _, private := range []string{"private-project", "private-key", "private-work", "example.invalid", "2222", ":read-only", "private-deploy-command"} {
		if strings.Contains(string(raw), private) {
			t.Fatalf("catalog disclosed %s", private)
		}
	}
}

func seedLegacyServer(s *connections.Store, p connections.ServerProfile) (connections.ServerProfile, error) {
	return p, s.Update(func(c *connections.Config) error { c.ServerProfiles = append(c.ServerProfiles, p); return nil })
}
