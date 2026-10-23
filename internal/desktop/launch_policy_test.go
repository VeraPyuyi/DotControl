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
		{"path-mismatch", StartupSnapshot{ManagedData: launchAgentXML("org.dotcontrol.agent", "/other", "/state")}, "managed_startup_unavailable"},
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
