package codexbridge

import (
	"runtime"
	"strings"
	"testing"
)

func TestExperimentPowerShellInvocationQuotesArgumentsAndWaitsForExit(t *testing.T) {
	command := ExperimentPowerShellCommand([]string{`C:\Program Files\dotcontrol.exe`, "--state", `C:\state with 'quote' $x;`, "experiment", "status"})
	for _, want := range []string{`'C:\Program Files\dotcontrol.exe'`, `'C:\state with ''quote'' $x;'`, "2>&1 |", "$LASTEXITCODE", "exit $dcExit"} {
		if !strings.Contains(command, want) {
			t.Fatalf("missing required PowerShell behavior %q: %s", want, command)
		}
	}
	if runtime.GOOS == "windows" {
		instructions := experimentInstructions(`C:\state`, "server-a", "fixture-a", strings.Repeat("a", 64))
		if !strings.Contains(instructions, "$LASTEXITCODE") || !strings.Contains(instructions, "PowerShell command exactly") {
			t.Fatal("official tool instructions omit the tested PowerShell command")
		}
	}
}
