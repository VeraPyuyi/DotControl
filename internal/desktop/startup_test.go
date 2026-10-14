package desktop

import (
	"strings"
	"testing"
)

func TestTaskPolicyUsesInteractiveLeastPrivilegeAndFailureOnlyRecovery(t *testing.T) {
	data := windowsTaskXML(`C:\App & tools\dotcontrol.exe`, `C:\Private & exact\state`, "S-1-5-21-123", true)
	for _, want := range []string{"<LogonType>InteractiveToken</LogonType>", "<RunLevel>LeastPrivilege</RunLevel>", "<MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>", "<AllowHardTerminate>false</AllowHardTerminate>", "<ExecutionTimeLimit>PT0S</ExecutionTimeLimit>", "background-run"} {
		if !strings.Contains(data, want) {
			t.Fatalf("missing policy %s", want)
		}
	}
	if strings.Contains(data, "RestartOnFailure") {
		t.Fatal("nested scheduler retries would multiply the supervisor budget")
	}
	s := StartupSnapshot{TaskXML: data, TaskEnabled: true}
	if !s.BelongsToState(`C:\Private & exact\state`) || s.BelongsToState(`C:\Private`) || !s.AutostartConfigured() {
		t.Fatal("task ownership/configuration not parsed exactly")
	}
	s.TaskXML = windowsTaskXML(`C:\App\dotcontrol.exe`, `C:\Private & exact\state`, "S-1-5-21-123", false)
	if s.AutostartConfigured() {
		t.Fatal("disabled login trigger became enabled")
	}
	s.Exists = true
	s.Value = `"other" --state "another" serve`
	if s.BelongsToState(`C:\Private & exact\state`) {
		t.Fatal("conflicting legacy Run entry accepted")
	}
}

func TestLaunchAgentFailureOnlyRecoveryAndExactArguments(t *testing.T) {
	data := launchAgentXML("org.dotcontrol.agent", "/App & tools/dotcontrol", "/Private & exact/state")
	for _, want := range []string{"<key>SuccessfulExit</key><false/>", "<key>ThrottleInterval</key><integer>60</integer>", "<string>--managed-start</string><string>launch-agent</string>"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("missing policy %s", want)
		}
	}
	s := StartupSnapshot{ManagedData: data, Loaded: true}
	if !s.Present() || !s.BelongsToState("/Private & exact/state") || s.BelongsToState("/Private") || s.AutostartConfigured() {
		t.Fatal("on-demand loaded registration lost disabled login choice")
	}
	s.Exists = true
	s.Data = data
	if !s.AutostartConfigured() {
		t.Fatal("login registration unrecognized")
	}
	s.Disabled = true
	if s.AutostartConfigured() {
		t.Fatal("launchctl disabled override ignored")
	}
}
