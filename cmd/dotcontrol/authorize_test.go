package main

import (
	"context"
	"github.com/dotcontrol/dotcontrol/internal/connections"
	"github.com/dotcontrol/dotcontrol/internal/onboard"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAuthorizationStatusRequiresBoundReceiptAndPreservesConnectionOnly(t *testing.T) {
	o := &runtimeOwner{dir: t.TempDir()}
	store := connections.NewStore(filepath.Join(o.dir, "connections"))
	p := connections.ServerProfile{ID: "example", Name: "Example", SSHAlias: "example.invalid"}
	if _, err := store.UpsertServer(p); err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("a", 64)
	preview := &authorizationPreview{ID: id, State: "awaiting_local_ssh", Expires: time.Now().Add(time.Minute), Fingerprint: "SHA256:chosen", profileID: p.ID, profileDigest: connections.ProfileDigest(p)}
	o.authorizations = map[string]*authorizationPreview{id: preview}
	request := onboard.ConnectionsRequest{Operation: "authorize_status", ProfileID: p.ID, AuthorizationID: id}
	result, err := o.Connections(context.Background(), request)
	if err != nil || result.(authorizationPreview).State != "awaiting_local_ssh" {
		t.Fatal("no receipt must stay waiting", err)
	}
	if err = store.SaveAuthorizationResult(id, connections.AuthorizationResult{ProfileDigest: preview.profileDigest, Fingerprint: preview.Fingerprint, Code: "ssh_connected"}); err != nil {
		t.Fatal(err)
	}
	result, err = o.Connections(context.Background(), request)
	if err != nil || result.(authorizationPreview).State != "ssh_connected" {
		t.Fatal("blank directory became ready", err)
	}
}

func TestSSHAuthorizationRequiresLocalSavedProfileAndRejectsChangedConsent(t *testing.T) {
	o := &runtimeOwner{dir: t.TempDir()}
	_, err := o.Connections(context.Background(), onboard.ConnectionsRequest{Operation: "authorize_prepare", ProfileID: "missing"})
	if err == nil || err.Error() != "server_profile_not_found" {
		t.Fatalf("%v", err)
	}
	p := connections.ServerProfile{ID: "example", Name: "Example", SSHAlias: "example.invalid", RemoteDir: "/tmp"}
	if _, err := connections.NewStore(filepath.Join(o.dir, "connections")).UpsertServer(p); err != nil {
		t.Fatal(err)
	}
	_, err = o.Connections(context.Background(), onboard.ConnectionsRequest{Operation: "authorize_start", ProfileID: "example"})
	if err == nil || err.Error() != "authorization_preview_required" {
		t.Fatalf("%v", err)
	}
}

func TestAuthorizationTraceClassifiesFailureWithoutKeepingRawOutput(t *testing.T) {
	trace := &authorizationTrace{}
	for _, part := range []string{"debug1: Authentications that can continue: publickey,password\n", "Connection closed by private", ".example port 22\n"} {
		trace.Write([]byte(part))
	}
	if got := trace.failureCode(255); got != "ssh_closed_before_authentication" {
		t.Fatal(got)
	}
	trace.Write([]byte("Authenticated to private.example using \"password\".\ndebug1: Sending command: private-command\n"))
	if got := trace.failureCode(74); got != "ssh_authorization_remote_safety_check_failed" {
		t.Fatal(got)
	}
	if got := trace.failureCode(255); got != "ssh_closed_after_authentication" {
		t.Fatal(got)
	}
	if trace.pending != "" {
		t.Fatal("raw completed lines retained")
	}
	trace.Write([]byte(strings.Repeat("x", 100000)))
	if len(trace.pending) > 4096 {
		t.Fatal("unbounded trace")
	}
	trace = &authorizationTrace{}
	trace.Write([]byte("x" + strings.Repeat("中", 40000)))
	trace.Write([]byte(strings.Repeat("文", 40000)))
	if len(trace.pending) > 4096 {
		t.Fatal("unbounded non-ASCII trace")
	}
	trace.Write([]byte("\nConnection closed\n"))
	if trace.failureCode(255) != "ssh_closed_before_authentication" || trace.pending != "" {
		t.Fatal("long-line recovery failed")
	}
	trace = &authorizationTrace{}
	trace.Write([]byte("Authenticated to jump.example using \"publickey\".\ndebug1: Authenticating to target.example:22 as 'user'\nPermission denied\nConnection closed by target.example\n"))
	if trace.failureCode(255) != "ssh_closed_before_authentication" {
		t.Fatal("jump host success was mistaken for target login")
	}
}
