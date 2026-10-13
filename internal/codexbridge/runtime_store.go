package codexbridge

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/dotcontrol/dotcontrol/internal/connections"
	"github.com/dotcontrol/dotcontrol/internal/engine"
	"github.com/dotcontrol/dotcontrol/internal/model"
)

// Acceptance is installation/platform-specific. All gates are closed by default.
// Enabling a gate is a release acceptance decision, not an inventory inference.
type PlatformAcceptance struct{ Terminal, NativeComputerUse bool }
type RuntimeConfig struct {
	ConnectionsDir string
	// Copied at startup; later changes require the protected idle-only setter.
	TerminalOnly    bool
	TerminalEnabled bool
	// Product entry points require explicit local opt-in even after platform
	// acceptance opens. Zero preserves the private legacy protocol test seam.
	TerminalOptInRequired bool
	TerminalProfileID     string
	TerminalServerID      string
	TerminalConfigHash    string
	Acceptance            map[string]PlatformAcceptance
	Engine                *engine.Engine
	OpenThread            func(context.Context, string) error
	SupportedVersions     []string
	// StartClient is the official stdio transport seam, used by protocol tests.
	StartClient func(context.Context, string, string) (*Client, error)
}
type runtimeState struct {
	stateDir          string
	cfg               RuntimeConfig
	file              *os.File
	chain             string
	jobs              map[string]*runtimeJob
	requests          map[string]string
	closing           bool
	wg                sync.WaitGroup
	connections       int
	nativeConnections int
}
type runtimeEntry struct {
	Version           int               `json:"version"`
	Job               model.CodexJob    `json:"job"`
	ProfileHash       string            `json:"profile_hash,omitempty"`
	ExperimentBinding string            `json:"experiment_binding,omitempty"`
	Phase             string            `json:"phase"`
	Requests          map[string]string `json:"requests"`
	CurrentRequestID  string            `json:"current_request_id"`
	HadResults        bool              `json:"had_results,omitempty"`
	LastReviewID      string            `json:"last_review_id,omitempty"`
	DesktopPending    bool              `json:"desktop_pending,omitempty"`
	NeedsLocalSetup   bool              `json:"needs_local_setup,omitempty"`
}
type runtimeRecord struct {
	Entry    runtimeEntry `json:"entry"`
	Previous string       `json:"previous"`
	MAC      string       `json:"mac"`
}
type runtimeJob struct {
	entry      runtimeEntry
	client     *Client
	lease      *engine.DelegationLease
	cancel     context.CancelFunc
	runContext context.Context
	active     bool
	// Only items that contributed text bytes or images are retained here. Thus
	// the dedupe set is bounded by maxResultText + maxResultImages, not turn length.
	resultItems map[[32]byte]struct{}
}

func cloneJob(j model.CodexJob) model.CodexJob {
	if j.Runtime == nil {
		return j
	}
	data, _ := json.Marshal(j)
	var copy model.CodexJob
	_ = json.Unmarshal(data, &copy)
	return copy
}
func journalEntry(e runtimeEntry) runtimeEntry {
	e.Job = cloneJob(e.Job)
	if r := e.Job.Runtime; r != nil {
		r.Text = ""
		r.Images = nil
		r.Evidence = nil
		r.Approvals = nil
		r.Experiment = nil
	}
	return e
}
func (b *Bridge) signRuntime(e runtimeEntry, previous string) string {
	data, _ := json.Marshal(e)
	return b.hash("runtime-v1:"+previous, data)
}
func (b *Bridge) openRuntime(cfg Config) error {
	rs := &runtimeState{stateDir: cfg.StateDir, jobs: map[string]*runtimeJob{}, requests: map[string]string{}}
	if cfg.Runtime != nil {
		rs.cfg = *cfg.Runtime
		rs.cfg.SupportedVersions = append([]string(nil), cfg.Runtime.SupportedVersions...)
		rs.cfg.Acceptance = map[string]PlatformAcceptance{}
		for p, a := range cfg.Runtime.Acceptance {
			rs.cfg.Acceptance[p] = a
		}
	}
	b.runtime = rs
	f, err := os.OpenFile(filepath.Join(cfg.StateDir, "codex-runtime-v1.jsonl"), os.O_CREATE|os.O_RDWR|os.O_APPEND, 0600)
	if err != nil {
		return failure("storage_unavailable")
	}
	rs.file = f
	fail := func() error { f.Close(); return failure("storage_corrupt") }
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 4096), 2<<20)
	for s.Scan() {
		var r runtimeRecord
		if json.Unmarshal(s.Bytes(), &r) != nil || r.Previous != rs.chain || !hmac.Equal([]byte(r.MAC), []byte(b.signRuntime(r.Entry, r.Previous))) {
			return fail()
		}
		e := r.Entry
		j := e.Job
		if e.Version != 1 || j.DeviceID != b.deviceID || !validID.MatchString(j.JobID) || !validID.MatchString(j.RequestID) || j.Runtime == nil || len(j.RequestHash) != 64 || len(j.PromptHash) != 64 || j.CreatedAt.IsZero() || j.UpdatedAt.Before(j.CreatedAt) {
			return fail()
		}
		if _, old := b.jobs[j.JobID]; old {
			return fail()
		}
		if previous := rs.jobs[j.JobID]; previous != nil {
			p := previous.entry
			if p.Job.RequestID != j.RequestID || p.Job.RequestHash != j.RequestHash || p.ProfileHash != e.ProfileHash || p.ExperimentBinding != e.ExperimentBinding || !p.Job.CreatedAt.Equal(j.CreatedAt) ||
				(p.Job.CancelRequested && !j.CancelRequested && !validCancellationReset(p, e)) {
				return fail()
			}
			for id, hash := range p.Requests {
				if e.Requests[id] != hash {
					return fail()
				}
			}
		}
		for id, hash := range e.Requests {
			if !validID.MatchString(id) || len(hash) != 64 {
				return fail()
			}
			if _, exists := b.requests[id]; exists {
				return fail()
			}
			if owner, exists := rs.requests[id]; exists && owner != j.JobID {
				return fail()
			}
			rs.requests[id] = j.JobID
		}
		rs.jobs[j.JobID] = &runtimeJob{entry: e}
		rs.chain = r.MAC
	}
	if s.Err() != nil {
		return fail()
	}
	if info, e := f.Stat(); e != nil {
		return fail()
	} else if info.Size() > 0 {
		last := []byte{0}
		if _, e = f.ReadAt(last, info.Size()-1); e != nil || last[0] != '\n' {
			return fail()
		}
	}
	if f.Sync() != nil || syncDirectory(cfg.StateDir) != nil {
		f.Close()
		return failure("storage_unavailable")
	}
	for _, j := range rs.jobs {
		j.entry.Job.Runtime.ResultStatus = "unavailable_after_restart"
		if j.entry.Job.Runtime.ImageDeliveryStatus != "" && j.entry.Job.Runtime.ImageDeliveryStatus != "not_required" {
			j.entry.Job.Runtime.ImageDeliveryStatus = "unavailable_after_restart"
		}
		if retiredRuntime(j.entry) {
			continue
		}
		if !runtimeTerminal(j.entry.Job.State) {
			j.entry.Job.State = "recovery_required"
			j.entry.Job.Code = "owned_thread_recovery_required"
		}
	}
	// Recovery is read-only and addresses only IDs already in the authenticated journal.
	for id, j := range rs.jobs {
		if retiredRuntime(j.entry) {
			continue
		}
		if (j.entry.Job.State == "recovery_required" || j.entry.DesktopPending) && j.entry.Job.ThreadID != "" && cfg.Runtime != nil {
			b.launchLocked(id, "", true)
		}
	}
	return nil
}

// Cancellation remains monotonic within a request. Only a fresh continuation
// intent following an authenticated official stop receipt may clear it; replay
// must not mistake an arbitrary later update for permission to run again.
func validCancellationReset(previous, next runtimeEntry) bool {
	if !continuableInterrupted(previous) || next.Version != 1 || next.Phase != "continue_intent" || next.DesktopPending ||
		next.Job.CancelRequested || next.Job.State != "queued" || next.Job.Code != "request_persisted" ||
		next.Job.ThreadID != previous.Job.ThreadID || next.Job.TurnID != "" ||
		next.Job.ProfileID != previous.Job.ProfileID || next.Job.ServerProfileID != previous.Job.ServerProfileID || next.Job.ExperimentID != previous.Job.ExperimentID ||
		next.Job.UpdatedAt.Before(previous.Job.UpdatedAt) || next.Job.Runtime == nil || next.Job.Runtime.Execution != "terminal" || next.Job.Runtime.TurnState != "" ||
		!validID.MatchString(next.CurrentRequestID) || len(next.Requests) != len(previous.Requests)+1 || len(next.Requests[next.CurrentRequestID]) != 64 || len(previous.Requests[previous.CurrentRequestID]) != 64 {
		return false
	}
	if _, used := previous.Requests[next.CurrentRequestID]; used {
		return false
	}
	for id, hash := range previous.Requests {
		if next.Requests[id] != hash {
			return false
		}
	}
	return true
}

func (b *Bridge) saveRuntimeLocked(j *runtimeJob) error {
	e := journalEntry(j.entry)
	r := runtimeRecord{Entry: e, Previous: b.runtime.chain, MAC: b.signRuntime(e, b.runtime.chain)}
	data, _ := json.Marshal(r)
	data = append(data, '\n')
	n, err := b.runtime.file.Write(data)
	if err != nil || n != len(data) || b.runtime.file.Sync() != nil {
		b.broken = true
		return failure("storage_unavailable")
	}
	b.runtime.chain = r.MAC
	for id := range e.Requests {
		b.runtime.requests[id] = e.Job.JobID
	}
	return nil
}
func retiredRuntime(e runtimeEntry) bool {
	return e.Job.ServerProfileID != "" || e.Job.ExperimentID != "" || e.ExperimentBinding != ""
}

func runtimeTerminal(state string) bool {
	switch state {
	case "completed", "failed", "cancelled", "blocked", "rejected", "local_takeover":
		return true
	}
	return false
}
func randomJobID() (string, error) {
	data := make([]byte, 24)
	if _, e := rand.Read(data); e != nil {
		return "", failure("storage_unavailable")
	}
	return "codex-" + hex.EncodeToString(data), nil
}
func (b *Bridge) acceptance() PlatformAcceptance { return b.runtime.cfg.Acceptance[runtime.GOOS] }
func (b *Bridge) runtimeCapabilities() model.CodexCapabilities {
	b.mu.Lock()
	defer b.mu.Unlock()
	c := DisabledCapabilities()
	if b.runtime == nil {
		return c
	}
	c.CodexTerminal = b.terminalCapabilityLocked()
	if b.runtime.connections > 0 {
		c.CodexConnection = model.Capability{Available: true, State: "connected"}
	}
	// Acceptance is necessary but a live connection is independently required.
	a := b.acceptance()
	terminalAccepted := a.Terminal && (!b.runtime.cfg.TerminalOptInRequired || (b.runtime.cfg.TerminalOnly && b.runtime.cfg.TerminalEnabled))
	if b.runtime.connections > 0 && (terminalAccepted || (a.NativeComputerUse && b.runtime.nativeConnections > 0)) {
		c.CodexDelegation = model.Capability{Available: true, State: "accepted"}
	}
	if b.runtime.nativeConnections > 0 && a.NativeComputerUse {
		c.OfficialComputerUse = model.Capability{Available: true, State: "accepted"}
	}
	if b.runtime.cfg.TerminalOnly {
		c.OfficialComputerUse = model.Capability{State: "disabled", Code: "terminal_only"}
		c.CodexDelegation = c.CodexTerminal
	}
	return c
}

// ConfigureTerminal is a trusted local configuration operation, never a remote
// tool. It cannot change policy beneath an active or unresolved owned turn.
func (b *Bridge) ConfigureTerminal(only, enabled bool, profileID, serverID, configHash string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.healthy(); err != nil {
		return err
	}
	if b.runtime.closing {
		return failure("closed")
	}
	for _, j := range b.runtime.jobs {
		if j.active || j.client != nil || j.lease != nil || j.entry.DesktopPending || !runtimeTerminal(j.entry.Job.State) {
			return failure("terminal_runtime_busy")
		}
	}
	if b.runtime.cfg.Engine != nil && !b.runtime.cfg.Engine.Device().Paused {
		return failure("terminal_mode_pause_required")
	}
	if enabled {
		if !only || !validID.MatchString(profileID) || serverID != "" || !terminalConfigHashPattern.MatchString(configHash) {
			return failure("needs_local_setup")
		}
		cfg, p, _, code := b.runtimeProfile(profileID, serverID)
		if code != "" {
			return failure(code)
		}
		if p.Execution != "terminal" || p.ApprovalPolicy != "never" {
			return failure("needs_local_setup")
		}
		if TerminalConfigHash(cfg) != configHash {
			return failure("terminal_configuration_changed")
		}
	}
	b.runtime.cfg.TerminalOnly, b.runtime.cfg.TerminalEnabled = only, enabled
	b.runtime.cfg.TerminalProfileID, b.runtime.cfg.TerminalServerID, b.runtime.cfg.TerminalConfigHash = profileID, serverID, configHash
	return nil
}

// TerminalOnly exposes the local admission mode without exposing a remote
// configuration operation or depending on eventual desktop driver cleanup.
func (b *Bridge) TerminalOnly() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.runtime != nil && b.runtime.cfg.TerminalOnly
}

func (b *Bridge) terminalCapabilityLocked() model.Capability {
	if b.closed || b.runtime.closing {
		return model.Capability{State: "unavailable", Code: "closed"}
	}
	if b.broken {
		return model.Capability{State: "unavailable", Code: "storage_unavailable"}
	}
	if (b.runtime.cfg.TerminalOnly && !b.runtime.cfg.TerminalEnabled) || (b.runtime.cfg.TerminalOptInRequired && (!b.runtime.cfg.TerminalOnly || !b.runtime.cfg.TerminalEnabled)) {
		return model.Capability{State: "disabled", Code: "terminal_disabled"}
	}
	if b.runtime.cfg.Engine != nil && b.runtime.cfg.Engine.Device().Paused {
		return model.Capability{State: "paused", Code: "device_paused"}
	}
	if b.runtime.cfg.ConnectionsDir == "" {
		return model.Capability{State: "disabled", Code: "codex_bridge_not_configured"}
	}
	if !b.acceptance().Terminal {
		return model.Capability{State: "unaccepted", Code: "runtime_acceptance_required"}
	}
	if b.runtime.cfg.TerminalOnly {
		if !validID.MatchString(b.runtime.cfg.TerminalProfileID) || !terminalConfigHashPattern.MatchString(b.runtime.cfg.TerminalConfigHash) {
			return model.Capability{State: "unavailable", Code: "needs_local_setup"}
		}
		cfg, p, s, code := b.runtimeProfile(b.runtime.cfg.TerminalProfileID, b.runtime.cfg.TerminalServerID)
		if code == "" {
			code = b.runtimeAdmission(cfg, p, s)
		}
		if code != "" {
			return model.Capability{State: "unavailable", Code: code}
		}
		return model.Capability{Available: true, State: "ready", Code: "local_configuration_ready"}
	}
	cfg, err := connections.NewStore(b.runtime.cfg.ConnectionsDir).Load()
	if err != nil {
		return model.Capability{State: "unavailable", Code: "connections_unavailable"}
	}
	code := "terminal_profile_not_configured"
	for _, p := range cfg.ExecutionProfiles {
		if p.Execution != "terminal" {
			continue
		}
		if p.ApprovalPolicy != "never" {
			code = "needs_local_setup"
			continue
		}
		_, _, _, reason := b.runtimeProfile(p.ID, "")
		if reason != "" {
			code = reason
			continue
		}
		return model.Capability{Available: true, State: "ready", Code: "local_configuration_ready"}
	}
	return model.Capability{State: "unavailable", Code: code}
}
func (b *Bridge) closeRuntime() {
	b.mu.Lock()
	if b.runtime == nil || b.runtime.closing {
		b.mu.Unlock()
		return
	}
	b.runtime.closing = true
	for _, j := range b.runtime.jobs {
		if j.cancel != nil {
			j.cancel()
		}
	}
	b.mu.Unlock()
	b.runtime.wg.Wait()
	b.mu.Lock()
	_ = b.runtime.file.Close()
	b.mu.Unlock()
}
