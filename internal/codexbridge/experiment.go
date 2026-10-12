package codexbridge

import (
	"context"
	"encoding/json"
	"os"
	"time"

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
	return " This task explicitly selects the bundled CPU fixture sum-1-to-1000000-v1 (one AWK process, one CPU, no GPU or installs). Use the official exec_command tool to invoke the installed helper using these exact argument arrays, with appropriate platform shell quoting: " + string(raw) + ". Query status first. Submit at most once when status is absent; the helper persists intent before launch. A repeated submit only queries an existing intent. Never run direct SSH, arbitrary deployment commands, or bypass helper ownership checks. On unknown status, query the same ID; never choose a new ID to replay uncertain work. Report helper evidence separately from model text. Cancelling or closing this Codex controller does not stop the server experiment. Call stop only when the user explicitly requests remote experiment stop. Stop is cooperative and never signals unverified or unrelated processes. Respect official human input and approval requests; never auto-approve."
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
