package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/config"
	"github.com/dotcontrol/dotcontrol/internal/connections"
	"github.com/dotcontrol/dotcontrol/internal/desktop"
	"github.com/dotcontrol/dotcontrol/internal/onboard"
	"golang.org/x/term"
)

type authorizationPreview struct {
	ID                                      string    `json:"authorization_id"`
	Server                                  string    `json:"server"`
	Port                                    int       `json:"port,omitempty"`
	Fingerprint                             string    `json:"fingerprint"`
	State                                   string    `json:"state"`
	Expires                                 time.Time `json:"expires_at"`
	profileID, profileDigest, publicKeyPath string
}

func savedServer(store *connections.Store, id string) (connections.ServerProfile, error) {
	cfg, err := store.Load()
	if err != nil {
		return connections.ServerProfile{}, err
	}
	for _, p := range cfg.ServerProfiles {
		if p.ID == id {
			return p, nil
		}
	}
	return connections.ServerProfile{}, errors.New("server_profile_not_found")
}
func (o *runtimeOwner) authorizeConnection(ctx context.Context, store *connections.Store, r onboard.ConnectionsRequest) (any, error) {
	p, err := savedServer(store, r.ProfileID)
	if err != nil {
		return nil, err
	}
	if r.Operation == "authorize_prepare" {
		key, err := connections.ReadPublicKey(r.PublicKeyPath)
		if err != nil {
			return nil, err
		}
		item := authorizationPreview{ID: config.Random(), Server: p.SSHAlias, Port: p.SSHPort, Fingerprint: key.Fingerprint, State: "ready_for_local_confirmation", Expires: time.Now().Add(10 * time.Minute), profileID: p.ID, profileDigest: connections.ProfileDigest(p), publicKeyPath: r.PublicKeyPath}
		o.mu.Lock()
		if o.authorizations == nil {
			o.authorizations = map[string]*authorizationPreview{}
		}
		for id, v := range o.authorizations {
			if time.Now().After(v.Expires) {
				delete(o.authorizations, id)
			}
		}
		o.authorizations[item.ID] = &item
		o.mu.Unlock()
		return item, nil
	}
	o.mu.Lock()
	item := o.authorizations[r.AuthorizationID]
	if item == nil || item.profileID != p.ID || item.profileDigest != connections.ProfileDigest(p) || time.Now().After(item.Expires) {
		o.mu.Unlock()
		return nil, errors.New("authorization_preview_required")
	}
	snapshot := *item
	if r.Operation == "authorize_status" {
		o.mu.Unlock()
		if snapshot.State == "awaiting_local_ssh" {
			result, e := store.ReadAuthorizationResult(snapshot.ID)
			if e == nil && result.ProfileDigest == snapshot.profileDigest && result.Fingerprint == snapshot.Fingerprint {
				o.mu.Lock()
				item.State = result.Code
				snapshot = *item
				o.mu.Unlock()
			}
		}
		return snapshot, nil
	}
	if item.State != "ready_for_local_confirmation" {
		o.mu.Unlock()
		return snapshot, nil
	}
	item.State = "awaiting_local_ssh"
	snapshot = *item
	o.mu.Unlock()
	key, err := connections.ReadPublicKey(snapshot.publicKeyPath)
	if err != nil || key.Fingerprint != snapshot.Fingerprint {
		return nil, errors.New("public_key_changed")
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, errors.New("local_launcher_unavailable")
	}
	args := []string{"--state", o.dir, "ssh-authorize", "--profile", p.ID, "--public-key", snapshot.publicKeyPath, "--fingerprint", snapshot.Fingerprint, "--profile-digest", snapshot.profileDigest, "--authorization-id", snapshot.ID}
	if err = desktop.OpenInteractive(exe, args); err != nil {
		o.mu.Lock()
		item.State = "local_terminal_failed"
		snapshot = *item
		o.mu.Unlock()
		return snapshot, err
	}
	return snapshot, nil
}

// This command may only run with a real local terminal. SSH owns its own hidden
// password/passphrase prompt. Neither stdin capture nor HTTP credential input is
// available, and only public-key material is passed as the remote command.
func authorizeCLI(ctx context.Context, dir string, args []string) (resultErr error) {
	f := flags("ssh-authorize")
	id := f.String("profile", "", "Saved server profile")
	pub := f.String("public-key", "", "Public key file reference")
	fingerprint := f.String("fingerprint", "", "Fingerprint shown in local preview")
	digest := f.String("profile-digest", "", "Saved profile binding")
	authorizationID := f.String("authorization-id", "", "One local authorization operation")
	if err := f.Parse(args); err != nil {
		return err
	}
	if err := desktop.PrepareInteractiveIO(); err != nil {
		return err
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return errors.New("local_interactive_terminal_required")
	}
	defer func() {
		if resultErr != nil {
			fmt.Println("Result:", resultErr.Error())
		}
		fmt.Println("\nPress Enter to close this local authorization window.")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	}()
	store := connections.NewStore(filepath.Join(dir, "connections"))
	resultCode := "ssh_authorization_failed"
	defer func() {
		if resultErr != nil {
			resultCode = resultErr.Error()
		}
		_ = store.SaveAuthorizationResult(*authorizationID, connections.AuthorizationResult{ProfileDigest: *digest, Fingerprint: *fingerprint, Code: resultCode})
	}()
	p, err := savedServer(store, *id)
	if err != nil {
		return err
	}
	key, err := connections.ReadPublicKey(*pub)
	if err != nil {
		return err
	}
	if *fingerprint != key.Fingerprint || *digest != connections.ProfileDigest(p) {
		return errors.New("authorization_preview_changed")
	}
	// Ask OpenSSH to validate the public key's wire structure before any login.
	keygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		return errors.New("ssh_keygen_unavailable")
	}
	if err = exec.CommandContext(ctx, keygen, "-l", "-E", "sha256", "-f", *pub).Run(); err != nil {
		return errors.New("invalid_public_key")
	}
	ssh, err := exec.LookPath("ssh")
	if err != nil {
		return errors.New("ssh_unavailable")
	}
	fmt.Printf("DotControl public-key authorization\nServer: %s  Port: %d (0 = SSH config)\nPublic key: %s\n", p.SSHAlias, p.SSHPort, key.Fingerprint)
	fmt.Println("Check the server host fingerprint in OpenSSH. Enter passwords only at its local hidden prompt. No SSH service configuration will be changed.")
	key.Proof = config.Random()
	argv, err := connections.AuthorizationArgs(p, key)
	if err != nil {
		return err
	}
	// Diagnostics stay in this interactive terminal. Only a coarse failure code
	// is persisted; authentication proof still requires the fresh remote marker
	// and independent strict public-key preflight below.
	argv = append([]string{"-o", "LogLevel=DEBUG1"}, argv...)
	cmd := exec.CommandContext(ctx, ssh, argv...)
	cmd.Stdin = os.Stdin
	proofOutput := &authorizationOutput{}
	cmd.Stdout = io.MultiWriter(os.Stdout, proofOutput)
	trace := &authorizationTrace{}
	cmd.Stderr = io.MultiWriter(os.Stderr, trace)
	if err = cmd.Run(); err != nil {
		trace.flush()
		exitCode := -1
		if cmd.ProcessState != nil {
			exitCode = cmd.ProcessState.ExitCode()
		}
		return errors.New(trace.failureCode(exitCode))
	}
	if !strings.Contains(proofOutput.text, "authorization_added:"+key.Proof+"\n") && !strings.Contains(proofOutput.text, "already_present:"+key.Proof+"\n") {
		return errors.New("ssh_authorization_unconfirmed")
	}
	report := connections.PreflightSSH(ctx, connections.AuthorizationVerificationProfile(p, *pub), connections.PreflightOptions{ExpectedFingerprint: key.Fingerprint})
	fmt.Println("Verification:", report.Code)
	if !report.Available {
		return errors.New(report.Code)
	}
	resultCode = report.Code
	return nil
}

// Parse bounded complete diagnostic lines and immediately discard their text.
// This is a troubleshooting hint, not trusted authentication or success proof.
type authorizationTrace struct {
	pending                                                string
	discarding, authenticated, sentCommand, closed, denied bool
}

func (w *authorizationTrace) Write(p []byte) (int, error) {
	for i, c := range p {
		if c == '\n' {
			w.flush()
			w.discarding = false
			continue
		}
		if w.discarding {
			continue
		}
		if len(w.pending) >= 4096 {
			if strings.HasPrefix(w.pending, "debug1: Sending command:") {
				w.sentCommand = true
			}
			w.pending = ""
			w.discarding = true
			continue
		}
		w.pending += string(p[i : i+1])
	}
	return len(p), nil
}
func (w *authorizationTrace) flush() {
	line := w.pending
	w.pending = ""
	// ProxyJump success is not target authentication. Start a new diagnostic
	// phase when OpenSSH moves to the next host.
	if strings.HasPrefix(line, "debug1: Authenticating to ") {
		w.authenticated, w.sentCommand, w.closed, w.denied = false, false, false, false
	}
	if strings.HasPrefix(line, "Authenticated to ") {
		w.authenticated = true
	}
	if strings.HasPrefix(line, "debug1: Sending command:") {
		w.sentCommand = true
	}
	if strings.Contains(line, "Connection closed") || strings.Contains(line, "Connection reset") {
		w.closed = true
	}
	if strings.Contains(line, "Permission denied") || strings.Contains(line, "Too many authentication failures") {
		w.denied = true
	}
}
func (w *authorizationTrace) failureCode(exitCode int) string {
	if w.authenticated && w.sentCommand {
		if exitCode == 74 {
			return "ssh_authorization_remote_safety_check_failed"
		}
		if exitCode == 75 {
			return "ssh_authorization_already_in_progress"
		}
	}
	if w.closed {
		if w.authenticated {
			return "ssh_closed_after_authentication"
		}
		return "ssh_closed_before_authentication"
	}
	if w.denied && !w.authenticated {
		return "ssh_auth_failed"
	}
	if w.authenticated {
		return "ssh_authorization_remote_command_failed"
	}
	return "ssh_authorization_failed_check_local_terminal"
}

// Only bounded remote stdout is inspected for a fresh public operation marker.
// OpenSSH password prompts use its console, never this writer.
type authorizationOutput struct{ text string }

func (w *authorizationOutput) Write(p []byte) (int, error) {
	n := len(p)
	if len(w.text) < 8192 {
		left := 8192 - len(w.text)
		if len(p) > left {
			p = p[:left]
		}
		w.text += string(p)
	}
	return n, nil
}
