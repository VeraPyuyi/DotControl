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

func TestLocalReviewForm(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is required for browser form regression checks")
	}
	out, err := exec.Command(node, "testdata/reviews.test.cjs", "web/reviews.js").CombinedOutput()
	if err != nil {
		t.Fatalf("local review form: %v\n%s", err, out)
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
