package connections

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

func sshProof(args []string) CommandResult {
	match := regexp.MustCompile(`DOTCONTROL_SSH_[a-f0-9]+`).FindString(args[len(args)-1])
	return CommandResult{Stdout: match}
}

func TestSSHPreflightRejectsForcedCommandSuccessAndWrongKey(t *testing.T) {
	for _, tc := range []struct {
		output           bool
		diagnostic, code string
	}{
		{false, "", "ssh_remote_check_unconfirmed"},
		{true, `debug1: Server accepts key: other ED25519 SHA256:other` + "\n" + `Authenticated to example using "publickey".`, "ssh_selected_key_unconfirmed"},
	} {
		r := PreflightSSH(context.Background(), ServerProfile{ID: "example", Name: "Example", SSHAlias: "example.invalid", RemoteDir: "/missing"}, PreflightOptions{ExpectedFingerprint: "SHA256:chosen", LookPath: func(string) (string, error) { return filepath.Join(t.TempDir(), "ssh"), nil }, Runner: func(_ context.Context, _ string, args []string) (CommandResult, error) {
			out := CommandResult{}
			if tc.output {
				out = sshProof(args)
			}
			out.Stderr = tc.diagnostic
			return out, nil
		}})
		if r.Available || r.Code != tc.code {
			t.Fatalf("false success %#v", r)
		}
	}
}

func TestFingerprintUsesFinalTargetNotProxyJumpAuthentication(t *testing.T) {
	diagnostic := `debug1: Server accepts key: jump ED25519 SHA256:chosen` + "\n" + `Authenticated to jump using "publickey".` + "\n" + `debug1: Server accepts key: target ED25519 SHA256:other` + "\n" + `Authenticated to target using "publickey".`
	if authenticatedFingerprint(diagnostic, "SHA256:chosen") {
		t.Fatal("jump key mistaken for target key")
	}
}

func TestSSHPreflightUsesStrictArgvAndQuotesRemoteDirectory(t *testing.T) {
	profile := ServerProfile{ID: "example", Name: "Private profile", SSHAlias: "example-build", RemoteDir: "/srv/a'$(touch BAD); project", IdentityFile: filepath.Join(t.TempDir(), "key reference"), DeployCommand: "NEVER_EXECUTE"}
	if err := os.WriteFile(profile.IdentityFile, []byte("test fixture, not a key"), 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "ssh")
	called := false
	report := PreflightSSH(context.Background(), profile, PreflightOptions{
		LookPath: func(name string) (string, error) {
			if name != "ssh" {
				t.Fatal(name)
			}
			return binary, nil
		},
		Runner: func(ctx context.Context, exe string, args []string) (CommandResult, error) {
			called = true
			if exe != binary {
				t.Fatal("wrong executable")
			}
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) > 20*time.Second {
				t.Fatal("no finite deadline")
			}
			for _, option := range []string{"BatchMode=yes", "StrictHostKeyChecking=yes", "CheckHostIP=no", "ForkAfterAuthentication=no", "UpdateHostKeys=no", "PasswordAuthentication=no", "KbdInteractiveAuthentication=no", "PreferredAuthentications=publickey", "ConnectTimeout=10", "NumberOfPasswordPrompts=0", "ForwardAgent=no", "ForwardX11=no", "ClearAllForwardings=yes", "PermitLocalCommand=no", "ControlPath=none", "ControlMaster=no"} {
				found := false
				for i := 1; i < len(args); i++ {
					if args[i-1] == "-o" && args[i] == option {
						found = true
					}
				}
				if !found {
					t.Fatalf("missing option %s", option)
				}
			}
			if !reflect.DeepEqual(args[len(args)-5:len(args)-1], []string{"-i", profile.IdentityFile, "--", profile.SSHAlias}) {
				t.Fatalf("unsafe destination argv: %#v", args)
			}
			command := args[len(args)-1]
			if !strings.Contains(command, `'/srv/a'"'"'$(touch BAD); project'`) || strings.Contains(command, profile.DeployCommand) {
				t.Fatal("unsafe remote command", command)
			}
			return sshProof(args), nil
		},
	})
	if !called || !report.Available || report.Code != "ssh_ready" {
		t.Fatalf("%#v", report)
	}
	b, _ := json.Marshal(report)
	for _, secret := range []string{profile.Name, profile.SSHAlias, profile.RemoteDir, profile.IdentityFile, binary} {
		if strings.Contains(string(b), secret) {
			t.Fatal("status exposed private reference")
		}
	}
}

func TestSSHPreflightClassifiesFailuresWithoutRawDiagnostics(t *testing.T) {
	cases := []struct {
		stderr string
		exit   int
		err    error
		code   string
	}{
		{"No ED25519 host key is known for secret.host and you have requested strict checking.", 255, errors.New("exit status 255"), "ssh_unknown_host"},
		{"WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED! private/key", 255, errors.New("exit status 255"), "ssh_host_key_changed"},
		{"secret@host: Permission denied (publickey).", 255, errors.New("exit status 255"), "ssh_auth_failed"},
		{"ssh: connect to host secret.host port 22: Connection refused", 255, errors.New("exit status 255"), "ssh_network_error"},
		{"Could not resolve hostname secret.host", 255, errors.New("exit status 255"), "ssh_network_error"},
		{"secret remote directory", 73, errors.New("exit status 73"), "ssh_remote_dir_unavailable"},
		{"private output", 255, context.DeadlineExceeded, "ssh_timeout"},
		{"private output", 255, context.Canceled, "ssh_cancelled"},
		{"secret/unclassified", 255, errors.New("private failure"), "ssh_preflight_failed"},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			got := PreflightSSH(context.Background(), ServerProfile{ID: "server", Name: "Example", SSHAlias: "example", RemoteDir: "/srv/example"}, PreflightOptions{
				LookPath: func(string) (string, error) { return filepath.Join(t.TempDir(), "ssh"), nil },
				Runner: func(context.Context, string, []string) (CommandResult, error) {
					return CommandResult{Stderr: tc.stderr, ExitCode: tc.exit}, tc.err
				},
			})
			if got.Available || got.Code != tc.code {
				t.Fatalf("%#v", got)
			}
			encoded, _ := json.Marshal(got)
			if strings.Contains(string(encoded), "secret") || strings.Contains(string(encoded), "private") {
				t.Fatal("raw output leaked")
			}
		})
	}
}

func TestSSHPreflightRejectsInvalidProfilesBeforeRunning(t *testing.T) {
	called := false
	got := PreflightSSH(context.Background(), ServerProfile{ID: "x", Name: "x", SSHAlias: "-oProxyCommand=evil", RemoteDir: "/srv/example"}, PreflightOptions{Runner: func(_ context.Context, _ string, args []string) (CommandResult, error) {
		called = true
		return sshProof(args), nil
	}})
	if called || got.Code != "invalid_ssh_alias" {
		t.Fatalf("unsafe profile invoked: %#v", got)
	}
}

func TestSSHPreflightTimeoutIsBounded(t *testing.T) {
	got := PreflightSSH(context.Background(), ServerProfile{ID: "x", Name: "x", SSHAlias: "example", RemoteDir: "/srv/example"}, PreflightOptions{
		Timeout:  time.Millisecond,
		LookPath: func(string) (string, error) { return filepath.Join(t.TempDir(), "ssh"), nil },
		Runner: func(ctx context.Context, _ string, _ []string) (CommandResult, error) {
			<-ctx.Done()
			return CommandResult{}, ctx.Err()
		},
	})
	if got.Code != "ssh_timeout" {
		t.Fatal(got)
	}
}

func TestSSHPreflightAllowsConnectionOnlyAndSeparatePort(t *testing.T) {
	got := PreflightSSH(context.Background(), ServerProfile{ID: "example", Name: "Example", SSHAlias: "root@example.invalid", SSHPort: 2222}, PreflightOptions{
		LookPath: func(string) (string, error) { return filepath.Join(t.TempDir(), "ssh"), nil },
		Runner: func(_ context.Context, _ string, args []string) (CommandResult, error) {
			if !reflect.DeepEqual(args[len(args)-5:], []string{"-p", "2222", "--", "root@example.invalid", args[len(args)-1]}) {
				t.Fatalf("wrong connection-only argv: %#v", args)
			}
			return sshProof(args), nil
		},
	})
	if !got.Available || got.Code != "ssh_connected" {
		t.Fatal(got)
	}
}

func TestSSHPreflightValidatesIdentityReferenceWithoutReadingKeyContent(t *testing.T) {
	profile := ServerProfile{ID: "example", Name: "Example", SSHAlias: "root@example.invalid", IdentityFile: filepath.Join(t.TempDir(), "missing-key")}
	called := false
	options := PreflightOptions{
		LookPath: func(string) (string, error) { return filepath.Join(t.TempDir(), "ssh"), nil },
		Runner: func(_ context.Context, _ string, args []string) (CommandResult, error) {
			called = true
			return sshProof(args), nil
		},
	}
	got := PreflightSSH(context.Background(), profile, options)
	if called || got.Code != "ssh_identity_unavailable" {
		t.Fatalf("missing identity ignored: %#v", got)
	}
	profile.IdentityFile = t.TempDir()
	got = PreflightSSH(context.Background(), profile, options)
	if called || got.Code != "ssh_identity_unavailable" {
		t.Fatalf("directory identity accepted: %#v", got)
	}
	profile.IdentityFile = filepath.Join(t.TempDir(), "reference")
	if err := os.WriteFile(profile.IdentityFile, []byte("not key material: package must not read or parse this"), 0600); err != nil {
		t.Fatal(err)
	}
	got = PreflightSSH(context.Background(), profile, options)
	if !called || !got.Available || got.Code != "ssh_connected" {
		t.Fatalf("metadata-readable reference rejected: %#v", got)
	}
}

func TestSSHPreflightDoesNotHideUnusableSelectedIdentityAfterAgentSuccess(t *testing.T) {
	key := filepath.Join(t.TempDir(), "reference")
	if err := os.WriteFile(key, []byte("test reference"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range []string{`Load key "private/path": invalid format`, `Load key "private/path": error in libcrypto`, `Warning: Identity file private/path not accessible: No such file or directory.`, `WARNING: UNPROTECTED PRIVATE KEY FILE!`} {
		got := PreflightSSH(context.Background(), ServerProfile{ID: "example", Name: "Example", SSHAlias: "root@example.invalid", IdentityFile: key}, PreflightOptions{
			LookPath: func(string) (string, error) { return filepath.Join(t.TempDir(), "ssh"), nil },
			Runner: func(context.Context, string, []string) (CommandResult, error) {
				return CommandResult{Stderr: diagnostic}, nil
			},
		})
		if got.Available || got.Code != "ssh_identity_unavailable" {
			t.Fatalf("ignored unusable identity: %#v", got)
		}
	}
}
