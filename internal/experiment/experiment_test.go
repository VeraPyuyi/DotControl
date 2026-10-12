package experiment

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotcontrol/dotcontrol/internal/connections"
)

func testManager(t *testing.T, remote Remote) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	_, err := connections.NewStore(filepath.Join(dir, "connections")).UpsertServer(connections.ServerProfile{ID: "fixture", Name: "Fictional fixture", SSHAlias: "example.invalid", RemoteDir: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	return New(dir, Options{Remote: remote}), dir
}
func response(r Request, state string) []byte {
	s := Status{ExperimentID: r.ExperimentID, Binding: r.Binding, Nonce: r.Nonce, State: state}
	if state == "completed" {
		z := 0
		s.ExitCode = &z
		s.Result = ExpectedResult
		s.ResultSHA256 = expectedHash()
		s.Expected = true
	}
	b, _ := json.Marshal(s)
	return b
}
func TestSubmitPersistsIntentBeforeLaunchAndRetryOnlyQueries(t *testing.T) {
	launches := 0
	var dir string
	remote := func(_ context.Context, _ connections.ServerProfile, r Request) ([]byte, error) {
		if r.Operation == "submit" {
			launches++
			if _, err := os.Stat(filepath.Join(dir, "experiments", "sum-1.json")); err != nil {
				t.Fatal("remote launch before durable intent", err)
			}
			return nil, errors.New("lost response secret endpoint")
		}
		if launches > 0 {
			return response(r, "completed"), nil
		}
		return response(r, "absent"), nil
	}
	m, d := testManager(t, remote)
	dir = d
	got, err := m.Run(context.Background(), "submit", "fixture", "sum-1")
	if err != nil || got.State != "completed" || !got.Expected {
		t.Fatalf("%+v %v", got, err)
	}
	// A new helper process sees the intent and only reads remote evidence.
	got, err = New(dir, Options{Remote: remote}).Run(context.Background(), "submit", "fixture", "sum-1")
	if err != nil || got.State != "completed" || launches != 1 {
		t.Fatalf("%+v %v launches=%d", got, err, launches)
	}
}
func TestUnknownThenAbsentNeverResubmits(t *testing.T) {
	launches := 0
	remote := func(_ context.Context, _ connections.ServerProfile, r Request) ([]byte, error) {
		if r.Operation == "submit" {
			launches++
			return nil, errors.New("secret network failure")
		}
		return response(r, "absent"), nil
	}
	m, dir := testManager(t, remote)
	for i := 0; i < 3; i++ {
		s, e := m.Run(context.Background(), "submit", "fixture", "lost-1")
		if e != nil || s.State != "outcome_unknown" || strings.Contains(s.Code, "secret") {
			t.Fatalf("%+v %v", s, e)
		}
		m = New(dir, Options{Remote: remote})
	}
	if launches != 1 {
		t.Fatalf("launches=%d", launches)
	}
}
func TestChangedProfileConflictsWithoutRemoteCall(t *testing.T) {
	calls := 0
	m, dir := testManager(t, func(_ context.Context, _ connections.ServerProfile, r Request) ([]byte, error) {
		calls++
		return response(r, "absent"), nil
	})
	_, _ = m.Run(context.Background(), "submit", "fixture", "stable")
	before := calls
	_, e := connections.NewStore(filepath.Join(dir, "connections")).UpsertServer(connections.ServerProfile{ID: "fixture", Name: "Changed", SSHAlias: "other.invalid", RemoteDir: "/tmp"})
	if e != nil {
		t.Fatal(e)
	}
	_, e = m.Run(context.Background(), "submit", "fixture", "stable")
	if e == nil || e.Error() != "experiment_binding_conflict" || calls != before {
		t.Fatalf("%v %d/%d", e, calls, before)
	}
}
func TestInvalidIDsAndTraversalNeverInvokeRemote(t *testing.T) {
	m, dir := testManager(t, func(context.Context, connections.ServerProfile, Request) ([]byte, error) {
		t.Fatal("remote called")
		return nil, nil
	})
	for _, id := range []string{"../escape", "a/b", "-x", "a:b", "", strings.Repeat("a", 81)} {
		if _, e := m.Run(context.Background(), "submit", "fixture", id); e == nil {
			t.Fatal(id)
		}
	}
	_, _ = connections.NewStore(filepath.Join(dir, "connections")).UpsertServer(connections.ServerProfile{ID: "fixture", Name: "Traversal", SSHAlias: "example.invalid", RemoteDir: "/tmp/../etc"})
	if _, e := m.Run(context.Background(), "submit", "fixture", "valid"); e == nil {
		t.Fatal("traversal accepted")
	}
}
func TestRemoteProofIsBoundedBoundAndIndependentlyVerified(t *testing.T) {
	for _, kind := range []string{"nonce", "binding", "hash", "exit", "output", "oversized", "unknown-field"} {
		t.Run(kind, func(t *testing.T) {
			m, _ := testManager(t, func(_ context.Context, _ connections.ServerProfile, r Request) ([]byte, error) {
				var s Status
				_ = json.Unmarshal(response(r, "completed"), &s)
				switch kind {
				case "nonce":
					s.Nonce = "wrong"
				case "binding":
					s.Binding = "wrong"
				case "hash":
					s.ResultSHA256 = strings.Repeat("0", 64)
				case "exit":
					v := 1
					s.ExitCode = &v
				case "output":
					s.Result = "secret log"
				case "oversized":
					return []byte(strings.Repeat("x", 8193)), nil
				case "unknown-field":
					return []byte(`{"raw_secret":"bad"}`), nil
				}
				b, _ := json.Marshal(s)
				return b, nil
			})
			s, e := m.Run(context.Background(), "status", "fixture", "proof")
			if e != nil || s.State != "outcome_unknown" || s.Result != "" || s.Expected {
				t.Fatalf("%+v %v", s, e)
			}
		})
	}
}
func TestStopRequiresExistingIntentAndNeverSubmits(t *testing.T) {
	var ops []string
	m, _ := testManager(t, func(_ context.Context, _ connections.ServerProfile, r Request) ([]byte, error) {
		ops = append(ops, r.Operation)
		return response(r, "absent"), nil
	})
	s, e := m.Run(context.Background(), "stop", "fixture", "never-started")
	if e != nil || s.Code != "stop_unavailable" || len(ops) != 0 {
		t.Fatalf("%+v %v %v", s, e, ops)
	}
}

func TestChangedFixtureDigestConflictsWithoutRemoteCall(t *testing.T) {
	m, dir := testManager(t, func(context.Context, connections.ServerProfile, Request) ([]byte, error) {
		t.Fatal("remote called")
		return nil, nil
	})
	cfg, _ := connections.NewStore(filepath.Join(dir, "connections")).Load()
	i := makeIntent(cfg.ServerProfiles[0], "old-fixture")
	i.CommandDigest = strings.Repeat("f", 64)
	i.Binding = ""
	raw, _ := json.Marshal(i)
	i.Binding = digest(raw)
	if e := m.createIntent(i); e != nil {
		t.Fatal(e)
	}
	_, e := m.Run(context.Background(), "submit", "fixture", "old-fixture")
	if e == nil || e.Error() != "experiment_binding_conflict" {
		t.Fatalf("wrong fixture conflict: %v", e)
	}
}

func TestPreparedBindingRejectsMidTurnProfileChangesBeforeAnySSHOrIntent(t *testing.T) {
	m, dir := testManager(t, func(context.Context, connections.ServerProfile, Request) ([]byte, error) {
		t.Fatal("changed binding reached SSH")
		return nil, nil
	})
	cfg, _ := connections.NewStore(filepath.Join(dir, "connections")).Load()
	binding, e := m.PrepareBinding(cfg.ServerProfiles[0], "accepted-a")
	if e != nil {
		t.Fatal(e)
	}
	if _, exists, e := m.readIntent("accepted-a"); e != nil || exists {
		t.Fatal("preparation consumed launch intent", exists, e)
	}
	changed := cfg.ServerProfiles[0]
	changed.SSHAlias = "different.invalid"
	if _, e = connections.NewStore(filepath.Join(dir, "connections")).UpsertServer(changed); e != nil {
		t.Fatal(e)
	}
	for _, op := range []string{"submit", "status", "stop"} {
		// Omitting the expected digest cannot bypass the authoritative local plan.
		for _, expected := range []string{"", binding} {
			checked := New(dir, Options{Remote: m.remote, ExpectedBinding: expected})
			if _, e = checked.Run(context.Background(), op, "fixture", "accepted-a"); e == nil || e.Error() != "experiment_binding_conflict" {
				t.Fatalf("%s expected=%q: %v", op, expected, e)
			}
		}
	}
	if _, exists, e := m.readIntent("accepted-a"); e != nil || exists {
		t.Fatal("conflict created intent", exists, e)
	}
}

func TestRequiredBindingMissingFailsClosedAndExistingIntentNeverBecomesRetryable(t *testing.T) {
	m, dir := testManager(t, func(context.Context, connections.ServerProfile, Request) ([]byte, error) {
		t.Fatal("unexpected remote call")
		return nil, nil
	})
	cfg, _ := connections.NewStore(filepath.Join(dir, "connections")).Load()
	p := cfg.ServerProfiles[0]
	expected := makeIntent(p, "missing").Binding
	checked := New(dir, Options{Remote: m.remote, ExpectedBinding: expected})
	for _, op := range []string{"submit", "status", "stop"} {
		if _, e := checked.Run(context.Background(), op, p.ID, "missing"); e == nil || e.Error() != "experiment_binding_missing" {
			t.Fatal(op, e)
		}
	}
	i := makeIntent(p, "old-intent")
	if e := m.createIntent(i); e != nil {
		t.Fatal(e)
	}
	if _, e := m.PrepareBinding(p, i.ExperimentID); e != nil {
		t.Fatal(e)
	}
	calls := 0
	checked = New(dir, Options{ExpectedBinding: i.Binding, Remote: func(_ context.Context, _ connections.ServerProfile, r Request) ([]byte, error) {
		calls++
		if r.Operation != "status" {
			t.Fatal("old intent resubmitted")
		}
		return response(r, "absent"), nil
	}})
	s, e := checked.Run(context.Background(), "submit", p.ID, i.ExperimentID)
	if e != nil || s.State != "outcome_unknown" || calls != 1 {
		t.Fatalf("%+v %v calls=%d", s, e, calls)
	}
}
