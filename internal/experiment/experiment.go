// Package experiment runs the bounded, explicitly selected CPU fixture through
// saved SSH references. A durable intent permits at most one submission attempt.
package experiment

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dotcontrol/dotcontrol/internal/connections"
)

const ExpectedResult = "500000500000\n"
const Fixture = "sum-1-to-1000000-v1"

var safeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,79}$`)

func ValidID(id string) bool    { return safeID.MatchString(id) }
func failure(code string) error { return errors.New(code) }
func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func expectedHash() string      { return digest([]byte(ExpectedResult)) }

type Status struct {
	ExperimentID string `json:"experiment_id"`
	State        string `json:"state"`
	Code         string `json:"code,omitempty"`
	Fixture      string `json:"fixture,omitempty"`
	ExitCode     *int   `json:"exit_code,omitempty"`
	Result       string `json:"result,omitempty"`
	ResultSHA256 string `json:"result_sha256,omitempty"`
	Expected     bool   `json:"expected_check"`
	// Binding and nonce are authenticated by SSH and checked before any response
	// is exposed. Neither contains profile contents or credentials.
	Binding string `json:"binding,omitempty"`
	Nonce   string `json:"nonce,omitempty"`
}
type Request struct{ Operation, ExperimentID, Binding, Nonce, Script string }
type Remote func(context.Context, connections.ServerProfile, Request) ([]byte, error)
type Options struct{ Remote Remote }
type Manager struct {
	dir    string
	remote Remote
}

func New(stateDir string, opts Options) *Manager {
	remote := opts.Remote
	if remote == nil {
		remote = func(ctx context.Context, p connections.ServerProfile, r Request) ([]byte, error) {
			return connections.RunRemote(ctx, p, r.Script, connections.RemoteOptions{})
		}
	}
	return &Manager{dir: stateDir, remote: remote}
}

type intent struct {
	Version       int                       `json:"version"`
	ExperimentID  string                    `json:"experiment_id"`
	Fixture       string                    `json:"fixture"`
	CommandDigest string                    `json:"command_digest"`
	Profile       connections.ServerProfile `json:"profile"`
	Binding       string                    `json:"binding"`
}

func makeIntent(p connections.ServerProfile, id string) intent {
	i := intent{Version: 1, ExperimentID: id, Fixture: Fixture, CommandDigest: digest([]byte(workerScript)), Profile: p}
	raw, _ := json.Marshal(i)
	i.Binding = digest(raw)
	return i
}
func unknown(id, code string) Status {
	return Status{ExperimentID: id, Fixture: Fixture, State: "outcome_unknown", Code: code}
}

func (m *Manager) Run(ctx context.Context, op, profileID, id string) (Status, error) {
	if !ValidID(id) || (op != "submit" && op != "status" && op != "stop") {
		return Status{}, failure("invalid_experiment_request")
	}
	cfg, e := connections.NewStore(filepath.Join(m.dir, "connections")).Load()
	if e != nil {
		return Status{}, failure("connections_unavailable")
	}
	var profile connections.ServerProfile
	for _, p := range cfg.ServerProfiles {
		if p.ID == profileID {
			profile = p
			break
		}
	}
	if profile.ID == "" {
		return Status{}, failure("server_profile_not_found")
	}
	if profile.RemoteDir == "" || profile.RemoteDir == "/" || path.Clean(profile.RemoteDir) != profile.RemoteDir || strings.Contains(profile.RemoteDir, "\\") || len(profile.RemoteDir) > 2048 {
		return Status{}, failure("invalid_experiment_remote_dir")
	}
	wanted := makeIntent(profile, id)
	stored, exists, e := m.readIntent(id)
	if e != nil {
		return Status{}, e
	}
	if exists && stored.Binding != wanted.Binding {
		return Status{}, failure("experiment_binding_conflict")
	}
	if ctx.Err() != nil {
		return unknown(id, "cancelled_before_remote_check"), nil
	}
	if op == "stop" && !exists {
		return Status{ExperimentID: id, Fixture: Fixture, State: "absent", Code: "stop_unavailable"}, nil
	}
	fresh := false
	if op == "submit" && !exists {
		if e = m.createIntent(wanted); e != nil {
			// A competing process may have atomically claimed the same ID. It alone
			// owns the launch attempt; this process may only query.
			stored, exists, e = m.readIntent(id)
			if e != nil || !exists {
				return Status{}, failure("experiment_storage_unavailable")
			}
			if stored.Binding != wanted.Binding {
				return Status{}, failure("experiment_binding_conflict")
			}
		} else {
			fresh = true
			exists = true
		}
	}
	// Query first on every submission, including retries and process restarts.
	status := m.query(ctx, profile, "status", wanted)
	if op == "submit" && fresh && status.State == "absent" {
		// The intent is already durable. Even a response loss cannot permit replay.
		_ = m.query(ctx, profile, "submit", wanted)
		status = m.query(ctx, profile, "status", wanted)
	} else if op == "stop" && status.State == "running" {
		status = m.query(ctx, profile, "stop", wanted)
		if status.State == "stop_requested" {
			return publicStatus(status), nil
		}
	}
	if exists && status.State == "absent" {
		status = unknown(id, "remote_record_absent_after_intent")
	}
	if op == "stop" && status.State != "completed" && status.State != "stopped" && status.State != "stop_requested" {
		status.Code = "stop_unavailable"
	}
	return publicStatus(status), nil
}
func publicStatus(s Status) Status { s.Binding = ""; s.Nonce = ""; s.Fixture = Fixture; return s }
func (m *Manager) query(ctx context.Context, p connections.ServerProfile, op string, i intent) Status {
	nonce := make([]byte, 16)
	if _, e := rand.Read(nonce); e != nil {
		return unknown(i.ExperimentID, "remote_read_unconfirmed")
	}
	r := Request{Operation: op, ExperimentID: i.ExperimentID, Binding: i.Binding, Nonce: hex.EncodeToString(nonce)}
	r.Script = remoteScript(p.RemoteDir, r)
	raw, e := m.remote(ctx, p, r)
	if e != nil {
		return unknown(i.ExperimentID, "remote_read_unconfirmed")
	}
	s, ok := decodeStatus(raw, r)
	if !ok {
		return unknown(i.ExperimentID, "remote_evidence_invalid")
	}
	return s
}
func decodeStatus(raw []byte, r Request) (Status, bool) {
	var s Status
	if len(raw) > 8192 {
		return s, false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&s) != nil || d.Decode(new(any)) != io.EOF || s.ExperimentID != r.ExperimentID || s.Binding != r.Binding || s.Nonce != r.Nonce || s.Code != "" || s.Fixture != "" {
		return Status{}, false
	}
	switch s.State {
	case "absent", "claimed", "running", "outcome_unknown", "unsafe_remote_state", "binding_conflict", "stop_requested", "stop_unavailable":
		if s.Result != "" || s.ResultSHA256 != "" || s.Expected || s.ExitCode != nil {
			return Status{}, false
		}
	case "completed":
		if s.ExitCode == nil || *s.ExitCode != 0 || s.Result != ExpectedResult || s.ResultSHA256 != expectedHash() || !s.Expected {
			return Status{}, false
		}
	case "failed", "stopped":
		if s.ExitCode == nil || *s.ExitCode < 1 || *s.ExitCode > 255 || s.Result != "" || s.ResultSHA256 != "" || s.Expected {
			return Status{}, false
		}
	default:
		return Status{}, false
	}
	return s, true
}
