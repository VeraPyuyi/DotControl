package connections

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofrs/flock"
)

func execution(t *testing.T) ExecutionProfile {
	t.Helper()
	return ExecutionProfile{ID: "project-one", Name: "Example project", ProjectDir: t.TempDir(), PermissionProfile: "workspace", Execution: "terminal"}
}

func TestNativeProfileRequiresSelectedApps(t *testing.T) {
	p := execution(t)
	p.Execution = "computer_use"
	if err := p.Validate(); err == nil || err.Error() != "invalid_allowed_apps" {
		t.Fatalf("native profile without apps accepted: %v", err)
	}
	p.AllowedApps = []string{"Calculator"}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestProfileDefaultsAndValidation(t *testing.T) {
	for _, id := range []string{":read-only", ":workspace", ":danger-full-access"} {
		p := execution(t)
		p.PermissionProfile = id
		if err := p.Validate(); err != nil {
			t.Fatalf("official reference %s rejected: %v", id, err)
		}
	}
	p := execution(t)
	c := Config{Version: 1, ExecutionProfiles: []ExecutionProfile{p}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.Normalize()
	if c.ExecutionProfiles[0].Presentation != "visible" {
		t.Fatal("new profiles must default to visible")
	}
	cases := []struct {
		name   string
		change func(*ExecutionProfile)
	}{
		{"relative project", func(p *ExecutionProfile) { p.ProjectDir = "relative" }},
		{"bad presentation", func(p *ExecutionProfile) { p.Presentation = "hidden" }},
		{"bad execution", func(p *ExecutionProfile) { p.Execution = "shell" }},
		{"invalid id", func(p *ExecutionProfile) { p.ID = "../profile" }},
		{"permission reference required", func(p *ExecutionProfile) { p.PermissionProfile = "" }},
		{"control app", func(p *ExecutionProfile) { p.AllowedApps = []string{"app\nsecret"} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cp := p
			tc.change(&cp)
			err := (Config{Version: 1, ExecutionProfiles: []ExecutionProfile{cp}}).Validate()
			if err == nil {
				t.Fatal("invalid profile accepted")
			}
			if strings.Contains(err.Error(), cp.ProjectDir) || strings.Contains(err.Error(), "secret") {
				t.Fatal("diagnostic leaks input")
			}
		})
	}
	c.ExecutionProfiles = append(c.ExecutionProfiles, p)
	if c.Validate() == nil {
		t.Fatal("duplicate ids accepted")
	}
	c = Config{Version: 2}
	if c.Validate() == nil {
		t.Fatal("future version accepted")
	}
}

func TestServerProfileRejectsSSHInjection(t *testing.T) {
	for _, alias := range []string{"-oProxyCommand=evil", "host;evil", "host\n-oFoo", "user@-host", "host $(evil)", "host:22", "", "host\x00"} {
		p := ServerProfile{ID: "server-one", Name: "Example server", SSHAlias: alias, RemoteDir: "/srv/example"}
		if (Config{Version: 1, ServerProfiles: []ServerProfile{p}}).Validate() == nil {
			t.Fatalf("accepted alias %q", alias)
		}
	}
	p := ServerProfile{ID: "server-one", Name: "Example server", SSHAlias: "example-build", RemoteDir: "/srv/project with 'quotes'"}
	if err := (Config{Version: 1, ServerProfiles: []ServerProfile{p}}).Validate(); err != nil {
		t.Fatal(err)
	}
	p.IdentityFile = "relative-key"
	if (Config{Version: 1, ServerProfiles: []ServerProfile{p}}).Validate() == nil {
		t.Fatal("relative identity accepted")
	}
	p.IdentityFile = filepath.Join(t.TempDir(), "key-reference")
	if err := (Config{Version: 1, ServerProfiles: []ServerProfile{p}}).Validate(); err != nil {
		t.Fatal(err)
	}
	p.RemoteDir = "relative"
	if (Config{Version: 1, ServerProfiles: []ServerProfile{p}}).Validate() == nil {
		t.Fatal("relative remote directory accepted")
	}
}

func TestServerEndpointAndOptionalRemoteDirectory(t *testing.T) {
	for _, endpoint := range []string{"root@example.invalid", "example-build", "192.0.2.1", "root@192.0.2.1", "2001:db8::1", "root@[2001:db8::1]"} {
		p := ServerProfile{ID: "server", Name: "Example", SSHAlias: endpoint, SSHPort: 2222}
		if err := p.Validate(); err != nil {
			t.Fatalf("rejected %s: %v", endpoint, err)
		}
	}
	for _, endpoint := range []string{"-root@example.invalid", "root@example.invalid;evil", "root@host@other", "[not-ipv6]", "root@$(evil)", "root@host:2222"} {
		p := ServerProfile{ID: "server", Name: "Example", SSHAlias: endpoint}
		if p.Validate() == nil {
			t.Fatalf("accepted %s", endpoint)
		}
	}
	for _, port := range []int{-1, 65536} {
		if (ServerProfile{ID: "server", Name: "Example", SSHAlias: "example-build", SSHPort: port}).Validate() == nil {
			t.Fatal("invalid port accepted")
		}
	}
}

func TestStoreEditsPreserveProfilesAndStableIDs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "connections")
	s := NewStore(dir)
	empty, err := s.Load()
	if err != nil || empty.Version != 1 {
		t.Fatalf("new store: %#v %v", empty, err)
	}
	p := execution(t)
	p.ID = ""
	saved, err := s.UpsertExecution(p)
	if err != nil || saved.ID == "" || saved.Presentation != "visible" {
		t.Fatalf("upsert: %#v %v", saved, err)
	}
	server, err := seedLegacyServer(s, ServerProfile{ID: "legacy", Name: "Example", SSHAlias: "example-build", RemoteDir: "/srv/example"})
	if err != nil || server.ID == "" {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "codex")
	if err := s.SetCodexExecutable(binary); err != nil {
		t.Fatal(err)
	}
	saved.Name = "Renamed project"
	again, err := NewStore(dir).UpsertExecution(saved)
	if err != nil || again.ID != saved.ID {
		t.Fatal("profile id changed", err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.ExecutionProfiles) != 1 || got.ExecutionProfiles[0].Name != saved.Name || len(got.ServerProfiles) != 1 || got.ServerProfiles[0].ID != server.ID || got.CodexExecutable != binary {
		t.Fatalf("lost profiles: %#v", got)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "connections.json"))
	invalid := saved
	invalid.ProjectDir = "relative"
	if _, err = s.UpsertExecution(invalid); err == nil {
		t.Fatal("invalid edit saved")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "connections.json"))
	if string(before) != string(after) {
		t.Fatal("failed edit changed persistent state")
	}
	if !json.Valid(after) {
		t.Fatal("invalid persisted JSON")
	}
	files, _ := filepath.Glob(filepath.Join(dir, ".connections-*"))
	if len(files) != 0 {
		t.Fatal("temporary files left behind")
	}
}

func TestStoreRejectsUnknownSecretFieldsAndMalformedData(t *testing.T) {
	for _, data := range []string{`{"version":1,"password":"never-store"}`, `{"version":1} {"version":1}`, `{"version":2}`, `{"version":1,"server_profiles":[{"private_key":"secret"}]}`} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "connections.json"), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := NewStore(dir).Load()
		if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), dir) {
			t.Fatal("unsafe corrupt-state diagnostic", err)
		}
		if err := NewStore(dir).SetCodexExecutable(""); err == nil {
			t.Fatal("corrupt state was silently replaced")
		}
		actual, _ := os.ReadFile(filepath.Join(dir, "connections.json"))
		if string(actual) != data {
			t.Fatal("corrupt state modified")
		}
	}
}

func TestStoreRefusesSymlinkState(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "target.json")
	if err := os.WriteFile(target, []byte(`{"version":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "connections.json")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, err := NewStore(dir).Load(); err == nil {
		t.Fatal("followed state symlink")
	}
	if err := NewStore(dir).SetCodexExecutable(""); err == nil {
		t.Fatal("replaced state symlink")
	}
}

func TestStoreRejectsConcurrentWriterWithoutChangingData(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	if err := store.SetCodexExecutable(""); err != nil {
		t.Fatal(err)
	}
	lock := flock.New(filepath.Join(dir, "connections.lock"))
	if err := lock.Lock(); err != nil {
		t.Fatal(err)
	}
	defer lock.Unlock()
	if err := NewStore(dir).SetCodexExecutable(filepath.Join(t.TempDir(), "codex")); err == nil || err.Error() != "connections_locked" {
		t.Fatal("writer was not serialized", err)
	}
}

func TestStoreRefusesHardlinkedState(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "target.json")
	if err := os.WriteFile(target, []byte(`{"version":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(target, filepath.Join(dir, "connections.json")); err != nil {
		t.Skip("hardlinks unavailable")
	}
	if _, err := NewStore(dir).Load(); err == nil {
		t.Fatal("read hardlinked state")
	}
	if err := NewStore(dir).SetCodexExecutable(""); err == nil {
		t.Fatal("overwrote hardlinked state")
	}
}

func seedLegacyServer(s *Store, p ServerProfile) (ServerProfile, error) {
	return p, s.Update(func(c *Config) error { c.ServerProfiles = append(c.ServerProfiles, p); return nil })
}
