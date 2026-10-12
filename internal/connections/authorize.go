package connections

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type PublicKey struct {
	Line        string `json:"-"`
	Fingerprint string `json:"fingerprint"`
	Proof       string `json:"-"`
}

// ReadPublicKey never follows a link or accepts options/private-key material.
// Comments are discarded so only the canonical public key reaches the server.
func ReadPublicKey(path string) (PublicKey, error) {
	if !absoluteLocal(path) || regularFile(path) != nil {
		return PublicKey{}, failure("invalid_public_key")
	}
	f, err := os.Open(path)
	if err != nil {
		return PublicKey{}, failure("public_key_unavailable")
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > 16384 || rejectHardlink(f, st) != nil {
		return PublicKey{}, failure("invalid_public_key")
	}
	b, err := io.ReadAll(io.LimitReader(f, 16385))
	if err != nil || len(b) > 16384 {
		return PublicKey{}, failure("invalid_public_key")
	}
	s := strings.TrimSpace(string(b))
	if strings.ContainsAny(s, "\r\n\x00") || strings.Contains(s, "PRIVATE KEY") {
		return PublicKey{}, failure("invalid_public_key")
	}
	fields := strings.Fields(s)
	if len(fields) < 2 {
		return PublicKey{}, failure("invalid_public_key")
	}
	switch fields[0] {
	case "ssh-ed25519", "ssh-rsa", "ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521", "sk-ssh-ed25519@openssh.com", "sk-ecdsa-sha2-nistp256@openssh.com":
	default:
		return PublicKey{}, failure("invalid_public_key")
	}
	blob, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil || len(blob) < 8 {
		return PublicKey{}, failure("invalid_public_key")
	}
	n := int(binary.BigEndian.Uint32(blob))
	if n > len(blob)-4 || string(blob[4:4+n]) != fields[0] {
		return PublicKey{}, failure("invalid_public_key")
	}
	if fields[0] == "ssh-ed25519" && (len(blob) != 4+n+4+32 || binary.BigEndian.Uint32(blob[4+n:]) != 32) {
		return PublicKey{}, failure("invalid_public_key")
	}
	digest := sha256.Sum256(blob)
	return PublicKey{Line: fields[0] + " " + base64.StdEncoding.EncodeToString(blob), Fingerprint: "SHA256:" + base64.RawStdEncoding.EncodeToString(digest[:])}, nil
}
func ProfileDigest(p ServerProfile) string {
	b, _ := json.Marshal(p)
	d := sha256.Sum256(b)
	return hex.EncodeToString(d[:])
}

// A blank identity means the selected public key must name its signing key,
// rather than letting a different default/agent identity prove authorization.
func AuthorizationVerificationProfile(p ServerProfile, publicPath string) ServerProfile {
	if p.IdentityFile != "" {
		return p
	}
	p.IdentityFile = publicPath
	if strings.HasSuffix(publicPath, ".pub") {
		private := strings.TrimSuffix(publicPath, ".pub")
		if readableIdentityReference(private) {
			p.IdentityFile = private
		}
	}
	return p
}
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'" }

// Linux/POSIX authorization only. Unsupported tools or unexpected ownership,
// permissions, links and an existing writer stop the operation without repair.
// Existing key restrictions are retained: an active entry of the same key skips
// append, including a restricted authorized_keys entry.
func authorizationCommand(k PublicKey) string {
	fields := strings.Fields(k.Line)
	if len(fields) != 2 {
		return "exit 74"
	}
	proof := k.Proof
	if proof != "" && !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(proof) {
		return "exit 74"
	}
	return `set -eu
umask 077
for tool in stat awk id mkdir rmdir; do command -v "$tool" >/dev/null || exit 74; done
u=$(id -u)
test -n "${HOME:-}" && test -d "$HOME" && ! test -L "$HOME" || exit 74
check_dir() { test -d "$1" && ! test -L "$1" && test "$(stat -c %u "$1")" = "$u" || return 1; m=$(stat -c %a "$1"); test $((0$m & 0022)) = 0; }
check_dir "$HOME" || exit 74
sd="$HOME/.ssh"
if ! test -e "$sd" && ! test -L "$sd"; then mkdir "$sd" || exit 74; fi
check_dir "$sd" || exit 74
lock="$sd/.dotcontrol-authorize-lock"
mkdir "$lock" || exit 75
trap 'rmdir "$lock"' EXIT HUP INT TERM
ak="$sd/authorized_keys"
if test -e "$ak" || test -L "$ak"; then
 test -f "$ak" && ! test -L "$ak" && test "$(stat -c %h "$ak")" = 1 && test "$(stat -c %u "$ak")" = "$u" || exit 74
 m=$(stat -c %a "$ak"); test $((0$m & 0022)) = 0 || exit 74
 if awk -v alg=` + shellQuote(fields[0]) + ` -v blob=` + shellQuote(fields[1]) + ` '
 /^[ \t]*#/ {next}
 { n=0; token=""; q=0; esc=0; delete f
   for(i=1;i<=length($0);i++) {
     c=substr($0,i,1)
     if(esc){token=token c;esc=0;continue}
     if(c=="\\" && q){esc=1;token=token c;continue}
     if(c=="\""){q=!q;token=token c;continue}
     if(c~/[ \t]/ && !q){if(token!=""){f[++n]=token;token="";if(n==3)break}}else{token=token c}
   }
   if(token!="" && n<3)f[++n]=token
   if(!q && ((f[1]==alg && f[2]==blob)||(f[2]==alg && f[3]==blob)))found=1
 } END {exit !found}' "$ak"; then printf '%s\n' ` + shellQuote("already_present:"+proof) + `; exit 0; fi
else
 (set -C; : > "$ak") || exit 74
fi
printf '\n%s\n' ` + shellQuote(k.Line) + ` >> "$ak"
printf '%s\n' ` + shellQuote("authorization_added:"+proof) + `
`
}

type AuthorizationResult struct {
	ProfileDigest string `json:"profile_digest"`
	Fingerprint   string `json:"fingerprint"`
	Code          string `json:"code"`
}

func (s *Store) SaveAuthorizationResult(id string, result AuthorizationResult) error {
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(id) {
		return failure("invalid_authorization_id")
	}
	if err := s.prepare(); err != nil {
		return err
	}
	path := filepath.Join(s.dir, "authorization-"+id+".json")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return failure("authorization_result_unavailable")
	}
	defer f.Close()
	if privatePath(path, false) != nil {
		return failure("authorization_result_unavailable")
	}
	if err = json.NewEncoder(f).Encode(result); err != nil {
		return failure("authorization_result_unavailable")
	}
	return f.Sync()
}
func (s *Store) ReadAuthorizationResult(id string) (AuthorizationResult, error) {
	var result AuthorizationResult
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(id) {
		return result, failure("invalid_authorization_id")
	}
	path := filepath.Join(s.dir, "authorization-"+id+".json")
	if regularFile(path) != nil {
		return result, failure("authorization_result_unavailable")
	}
	f, err := os.Open(path)
	if err != nil {
		return result, failure("authorization_result_pending")
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() > 2048 || rejectHardlink(f, st) != nil {
		return result, failure("authorization_result_unavailable")
	}
	if json.NewDecoder(io.LimitReader(f, 2049)).Decode(&result) != nil {
		return result, failure("authorization_result_pending")
	}
	return result, nil
}
func AuthorizationArgs(p ServerProfile, k PublicKey) ([]string, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if k.Line == "" {
		return nil, failure("invalid_public_key")
	}
	args := []string{"-T", "-o", "BatchMode=no", "-o", "StrictHostKeyChecking=ask", "-o", "UpdateHostKeys=no", "-o", "ForwardAgent=no", "-o", "ForwardX11=no", "-o", "ClearAllForwardings=yes", "-o", "PermitLocalCommand=no", "-o", "ControlMaster=no", "-o", "ControlPath=none", "-o", "ControlPersist=no", "-o", "ForkAfterAuthentication=no", "-o", "ConnectTimeout=15", "-o", "ConnectionAttempts=1"}
	if p.IdentityFile != "" {
		args = append(args, "-i", p.IdentityFile)
	}
	if p.SSHPort != 0 {
		args = append(args, "-p", strconv.Itoa(p.SSHPort))
	}
	return append(args, "--", p.SSHAlias, "sh -c "+shellQuote(authorizationCommand(k))), nil
}
