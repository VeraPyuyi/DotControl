package desktop

import (
	"path/filepath"
	"testing"
)

func TestProgramReferenceGuardIncludesDescendantsAndStartup(t *testing.T) {
	root := filepath.Join(t.TempDir(), "version")
	exe := filepath.Join(root, "dotcontrol.exe")
	for _, tc := range []struct {
		name      string
		processes []programProcess
		startup   string
		want      bool
	}{
		{"running", []programProcess{{PID: 4, Executable: exe}}, "", true},
		{"child transport", []programProcess{{PID: 5, Executable: filepath.Join(root, "tunnel-client.exe")}}, "", true},
		{"unknown executable", []programProcess{{PID: 6, Name: "dotcontrol.exe"}}, "", true},
		{"startup", nil, `"` + exe + `" --state "retained" serve`, true},
		{"shared mac app", []programProcess{{PID: 7, Executable: "/Applications/CuaDriver.app/Contents/MacOS/cua-driver"}}, "", false},
		{"sibling", []programProcess{{PID: 8, Executable: exe + "-sibling"}}, "", true},
		{"other generation", []programProcess{{PID: 9, Executable: filepath.Join(root+"-other", "dotcontrol.exe")}}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkProgramReferences(root, tc.processes, tc.startup)
			if (err != nil) != tc.want {
				t.Fatalf("guard=%v want blocked=%v", err, tc.want)
			}
		})
	}
}
