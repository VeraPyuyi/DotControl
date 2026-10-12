package connections

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRemoteUsesStrictSavedReferencesAndRedactsErrors(t *testing.T) {
	p := ServerProfile{ID: "fixture", Name: "Fixture", SSHAlias: "example.invalid", SSHPort: 2222, RemoteDir: "/tmp"}
	called := false
	_, e := RunRemote(context.Background(), p, "printf 'safe'", RemoteOptions{LookPath: func(string) (string, error) { return filepath.Join(t.TempDir(), "ssh"), nil }, Runner: func(_ context.Context, _ string, args []string) (CommandResult, error) {
		called = true
		joined := strings.Join(args, " ")
		for _, value := range []string{"BatchMode=yes", "StrictHostKeyChecking=yes", "PasswordAuthentication=no", "KbdInteractiveAuthentication=no", "ControlPath=none", "ForwardAgent=no", "PermitLocalCommand=no", "-p 2222", "example.invalid"} {
			if !strings.Contains(joined, value) {
				t.Error(value)
			}
		}
		return CommandResult{Stdout: "private", StdoutTruncated: true}, nil
	}})
	if !called || e == nil || strings.Contains(e.Error(), "private") {
		t.Fatalf("%v %v", called, e)
	}
}
