package codexbridge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/dotcontrol/dotcontrol/internal/experiment"
	"github.com/dotcontrol/dotcontrol/internal/model"
)

func experimentInstructions(stateDir, profile, id, binding string) string {
	binary, _ := os.Executable()
	commands := map[string][]string{}
	for _, op := range []string{"status", "submit", "stop"} {
		commands[op] = []string{binary, "--state", stateDir, "experiment", op, "--profile", profile, "--experiment-id", id, "--binding", binding}
	}
	raw, _ := json.Marshal(commands)
	shellInstructions := ""
	if runtime.GOOS == "windows" {
		powershell := map[string]string{}
		for op, args := range commands {
			powershell[op] = ExperimentPowerShellCommand(args)
		}
		encoded, _ := json.Marshal(powershell)
		shellInstructions = " On Windows use the matching PowerShell command exactly in exec_command: " + string(encoded) + ". Its encoded payload is the exact UTF-8 JSON argument array shown above, protecting Unicode paths from shell parsing. The helper is a GUI-subsystem executable; the explicit pipeline waits for it, captures JSON/errors, and preserves its actual exit code. A bare PowerShell invocation can return early with empty output. Empty output or shell exit zero alone is not experiment evidence."
	}
	return " This task explicitly selects the bundled CPU fixture sum-1-to-1000000-v1 (one AWK process, one CPU, no GPU or installs). Use the official exec_command tool to invoke the installed helper using these exact argument arrays, with appropriate platform shell quoting: " + string(raw) + "." + shellInstructions + " Query status first. Submit at most once when status is absent; the helper persists intent before launch. A repeated submit only queries an existing intent. Never run direct SSH, arbitrary deployment commands, or bypass helper ownership checks. On unknown status, query the same ID; never choose a new ID to replay uncertain work. Report helper evidence separately from model text. Cancelling or closing this Codex controller does not stop the server experiment. Call stop only when the user explicitly requests remote experiment stop. Stop is cooperative and never signals unverified or unrelated processes. Respect official human input and approval requests; never auto-approve."
}

// ExperimentPowerShellCommand renders the invocation used in task instructions.
// An explicit pipeline is required for PowerShell to wait for a Windows GUI
// executable. Converting error records to strings preserves helper diagnostics
// without PowerShell's invocation/path decoration, and exit propagates failure.
// Encode the complete argument array as UTF-8 JSON: PowerShell recognizes smart
// quotes as syntax even inside ASCII-quoted literals, so apostrophe escaping is
// insufficient. Decoded strings are passed as values, never evaluated as code.
func ExperimentPowerShellCommand(arguments []string) string {
	if len(arguments) == 0 {
		return "throw 'invalid_experiment_command'"
	}
	raw, _ := json.Marshal(arguments)
	encoded := base64.StdEncoding.EncodeToString(raw)
	// Show the exact reversible JSON argv to a human reviewing this command.
	// ASCII escapes prevent every Unicode quote/line separator from becoming
	// shell syntax. JSON has already escaped literal CR/LF and other controls.
	var display strings.Builder
	for _, r := range string(raw) {
		switch {
		case r <= 127:
			display.WriteRune(r)
		case r <= 0xffff:
			fmt.Fprintf(&display, `\u%04x`, r)
		default:
			high, low := utf16.EncodeRune(r)
			fmt.Fprintf(&display, `\u%04x\u%04x`, high, low)
		}
	}
	return "# DotControl helper argv: " + display.String() + "\n" + "$ErrorActionPreference = 'Continue'; $PSNativeCommandUseErrorActionPreference = $false; [string[]]$dcCommand = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('" + encoded + "')) | ConvertFrom-Json; $dcExecutable = $dcCommand[0]; $dcArgs = @($dcCommand | Select-Object -Skip 1); $dcOutput = @(& $dcExecutable @dcArgs 2>&1 | ForEach-Object { $_.ToString() }); $dcExit = $LASTEXITCODE; foreach ($dcLine in $dcOutput) { [Console]::Out.WriteLine($dcLine) }; if ($null -eq $dcExit) { [Console]::Error.WriteLine('experiment_exit_unconfirmed'); exit 1 }; exit $dcExit"
}

// Readback is independent of the model's commands and prose. It changes only
// this returned snapshot, preserving historic journal receipts and their hashes.
func (b *Bridge) experimentJob(j model.CodexJob) model.CodexJob {
	if j.ExperimentID == "" || j.Runtime == nil || j.State == "blocked" || j.State == "rejected" {
		return j
	}
	read := b.runtime.cfg.ExperimentStatus
	b.mu.Lock()
	binding := ""
	if owned := b.runtime.jobs[j.JobID]; owned != nil {
		binding = owned.entry.ExperimentBinding
	}
	b.mu.Unlock()
	if read == nil {
		read = func(ctx context.Context, profile, id, expected string) (experiment.Status, error) {
			manager := experiment.New(b.runtime.stateDir, experiment.Options{ExpectedBinding: expected})
			return manager.Run(ctx, "status", profile, id)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s := experiment.Status{ExperimentID: j.ExperimentID, Fixture: experiment.Fixture, State: "outcome_unknown", Code: "experiment_binding_missing"}
	var e error
	if binding != "" {
		s, e = read(ctx, j.ServerProfileID, j.ExperimentID, binding)
	}
	if e != nil {
		code := "experiment_read_unavailable"
		switch e.Error() {
		case "experiment_binding_missing", "experiment_binding_conflict":
			code = e.Error()
		}
		s = experiment.Status{ExperimentID: j.ExperimentID, Fixture: experiment.Fixture, State: "outcome_unknown", Code: code}
	}
	info := &model.CodexExperimentInfo{ExperimentID: j.ExperimentID, State: s.State, Code: s.Code, Fixture: experiment.Fixture, ExitCode: s.ExitCode, Result: s.Result, ResultSHA256: s.ResultSHA256, Expected: s.Expected}
	j.Runtime.Experiment = info
	j.Runtime.Verification = "unverified"
	j.Runtime.ResultStatus = "experiment_" + s.State
	if s.State == "completed" && s.Expected && s.ExitCode != nil && *s.ExitCode == 0 {
		j.Runtime.Verification = "verified_fixture"
		j.Runtime.ResultStatus = "experiment_verified"
	}
	j.Runtime.Evidence = append(j.Runtime.Evidence, model.CodexEvidence{Kind: "experiment", ItemID: j.ExperimentID, Status: s.State, ExitCode: s.ExitCode, Source: "independent_ssh_helper_readback", Output: s.Result})
	return j
}
