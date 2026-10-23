package desktop

import "testing"

func TestLaunchPolicyRejectsDisabledAndForeignDefinitions(t *testing.T) {
	data := launchAgentXML("org.dotcontrol.agent", "/agent", "/state")
	for _, tc := range []struct {
		name     string
		snapshot StartupSnapshot
		want     string
	}{
		{"unregistered", StartupSnapshot{ManagedData: data}, "bootstrap"},
		{"registered-stopped", StartupSnapshot{ManagedData: data, Loaded: true}, "kickstart"},
		{"running", StartupSnapshot{ManagedData: data, Loaded: true, LoadedText: "pid = 42\n"}, ""},
		{"disabled-loaded", StartupSnapshot{ManagedData: data, Loaded: true, Disabled: true}, "managed_startup_disabled"},
		{"disabled-running", StartupSnapshot{ManagedData: data, Loaded: true, Disabled: true, LoadedText: "pid = 42\n"}, "managed_startup_disabled"},
		{"path-mismatch", StartupSnapshot{ManagedData: launchAgentXML("org.dotcontrol.agent", "/other", "/state")}, "conflicting_definition_paths"},
		{"old-definition", StartupSnapshot{ManagedData: data, Data: launchAgentXML("org.dotcontrol.agent", "/old", "/foreign"), Exists: true}, "managed_startup_unavailable"},
		{"missing-definition", StartupSnapshot{Loaded: true}, "managed_startup_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			action, err := tc.snapshot.launchStartAction("/agent", "/state")
			got := action
			if err != nil {
				got = err.Error()
			}
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}
func TestLaunchPolicyChecksAllDefinitionPaths(t *testing.T) {
	current := launchAgentXML("org.dotcontrol.agent", "/agent", "/state")
	old := launchAgentXML("org.dotcontrol.agent", "/old", "/state")
	for _, tc := range []struct {
		name           string
		login, managed []byte
		loaded         string
		want           string
	}{
		{"different-paths-loaded-login", old, current, "path = /login.plist\nprogram = /old\npid = 42\n", "conflicting_definition_paths"},
		{"different-paths-loaded-background", current, old, "path = /background.plist\nprogram = /old\npid = 42\n", "conflicting_definition_paths"},
		{"same-path-loaded-login", current, current, "path = /login.plist\nprogram = /agent\npid = 42\n", ""},
		{"same-path-loaded-background", current, current, "path = /background.plist\nprogram = /agent\npid = 42\n", ""},
		{"stale-login-unloaded", old, current, "", "conflicting_definition_paths"},
		{"stale-background-unloaded", current, old, "", "conflicting_definition_paths"},
		{"stale-loaded-program", current, current, "path = /login.plist\nprogram = /old\npid = 42\n", "conflicting_definition_paths"},
		{"same-path-unloaded", current, current, "", "bootstrap"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := StartupSnapshot{Exists: true, Data: tc.login, ManagedData: tc.managed, Loaded: tc.loaded != "", LoadedText: tc.loaded}
			action, err := s.launchStartAction("/agent", "/state")
			got := action
			if err != nil {
				got = err.Error()
			}
			if got != tc.want {
				t.Fatalf("got %q want %q (action %q)", got, tc.want, action)
			}
			if tc.want == "conflicting_definition_paths" && s.matchesExecutable("/agent") {
				t.Fatal("conflicting snapshot matches target executable")
			}
		})
	}
}

func TestLaunchRegistrationIsNotRunningEvidence(t *testing.T) {
	for _, tc := range []struct {
		text string
		want bool
	}{{"", false}, {"pid = 0\n", false}, {"pid = invalid\n", false}, {"pid = 42\n", true}} {
		s := StartupSnapshot{Loaded: true, LoadedText: tc.text}
		if s.launchRunning() != tc.want {
			t.Fatalf("running for %q", tc.text)
		}
	}
}
