package onboard

import (
	"os/exec"
	"testing"
)

func TestNetworkStatusAndSettingsUI(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	if out, err := exec.Command(node, "testdata/network.test.cjs", "web/app.js").CombinedOutput(); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
}

func TestConsoleNavigation(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js required")
	}
	for _, name := range []string{"console", "history"} {
		if out, err := exec.Command(node, "testdata/"+name+".test.cjs", "web/"+name+".js").CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", name, err, out)
		}
	}
}

func TestTerminalOnboarding(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js required")
	}
	if out, err := exec.Command(node, "testdata/terminal.test.cjs", "web/app.js").CombinedOutput(); err != nil {
		t.Fatalf("terminal UI: %v\n%s", err, out)
	}
}

func TestWizardSessionRecovery(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js required")
	}
	if out, err := exec.Command(node, "testdata/session.test.cjs", "web/app.js").CombinedOutput(); err != nil {
		t.Fatalf("session recovery: %v\n%s", err, out)
	}
}
