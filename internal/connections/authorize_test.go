package connections

import (
	"encoding/base64"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixturePublicKey() string {
	b := make([]byte, 4+len("ssh-ed25519")+4+32)
	binary.BigEndian.PutUint32(b, uint32(len("ssh-ed25519")))
	copy(b[4:], "ssh-ed25519")
	binary.BigEndian.PutUint32(b[4+len("ssh-ed25519"):], 32)
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(b)
}
func TestAuthorizationReadsOnlySinglePublicKey(t *testing.T) {
	p := filepath.Join(t.TempDir(), "public key.pub")
	for _, bad := range []string{"-----BEGIN OPENSSH PRIVATE KEY-----", fixturePublicKey() + "\n" + fixturePublicKey(), "command=evil " + fixturePublicKey(), "ssh-rsa not-base64"} {
		_ = os.WriteFile(p, []byte(bad), 0600)
		if _, err := ReadPublicKey(p); err == nil {
			t.Fatal("accepted non-public or ambiguous key")
		}
	}
	_ = os.WriteFile(p, []byte(fixturePublicKey()+" person@example\n"), 0600)
	k, err := ReadPublicKey(p)
	if err != nil {
		t.Fatal(err)
	}
	if k.Line != fixturePublicKey() || !strings.HasPrefix(k.Fingerprint, "SHA256:") {
		t.Fatal("wrong canonical key")
	}
}
func TestAuthorizationCommandPreservesExistingKeysAndStrictPolicy(t *testing.T) {
	command := authorizationCommand(PublicKey{Line: fixturePublicKey()})
	for _, wanted := range []string{"test -L", "stat -c", "mkdir", "authorized_keys", "already_present", "authorization_added", "rmdir", "umask 077"} {
		if !strings.Contains(command, wanted) {
			t.Fatal("missing safety check", wanted)
		}
	}
	for _, bad := range []string{"chmod", "sshd_config", "rm -rf"} {
		if strings.Contains(command, bad) {
			t.Fatal("destructive authorization script", bad)
		}
	}
	if !strings.Contains(command, `(set -C; : > "$ak")`) {
		t.Fatal("new file must use noclobber")
	}
}
func TestAuthorizationArgumentsKeepHostVerificationAndNoSecretChannels(t *testing.T) {
	p := ServerProfile{ID: "example", Name: "Example", SSHAlias: "tester@example.invalid", SSHPort: 2222, RemoteDir: "/tmp"}
	args, err := AuthorizationArgs(p, PublicKey{Line: fixturePublicKey()})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, v := range []string{"StrictHostKeyChecking=ask", "ForwardAgent=no", "PermitLocalCommand=no", "ControlPath=none", "-p 2222", "-- tester@example.invalid"} {
		if !strings.Contains(joined, v) {
			t.Fatal("missing", v)
		}
	}
	if strings.Contains(joined, "StrictHostKeyChecking=no") || strings.Contains(joined, "BatchMode=yes") {
		t.Fatal("unsafe or noninteractive flags")
	}
	p.SSHAlias = "-oProxyCommand=bad"
	if _, err = AuthorizationArgs(p, PublicKey{Line: fixturePublicKey()}); err == nil {
		t.Fatal("option injection")
	}
}

func TestAuthorizationVerificationSelectsChosenPublicKey(t *testing.T) {
	pub := filepath.Join(t.TempDir(), "selected.pub")
	p := ServerProfile{ID: "example", Name: "Example", SSHAlias: "example.invalid"}
	if got := AuthorizationVerificationProfile(p, pub); got.IdentityFile != pub {
		t.Fatal("did not select agent key")
	}
	private := strings.TrimSuffix(pub, ".pub")
	_ = os.WriteFile(private, []byte("metadata only fixture"), 0600)
	if got := AuthorizationVerificationProfile(p, pub); got.IdentityFile != private {
		t.Fatal("did not select corresponding local reference")
	}
	p.IdentityFile = filepath.Join(t.TempDir(), "explicit")
	if got := AuthorizationVerificationProfile(p, pub); got.IdentityFile != p.IdentityFile {
		t.Fatal("overrode explicit user reference")
	}
}
