// Package codexbridge provides owned delegation records and an official App Server
// protocol client. Desktop execution remains disabled pending native acceptance.
package codexbridge

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/model"
	"github.com/gofrs/flock"
)

type Config struct {
	DeviceID, StateDir string
	Runtime            *RuntimeConfig
}
type Bridge struct {
	mu             sync.Mutex
	deviceID       string
	file           *os.File
	lock           *flock.Flock
	key            []byte
	chain          string
	jobs           map[string]model.CodexJob
	requests       map[string]string
	closed, broken bool
	runtime        *runtimeState
}
type record struct {
	Job      model.CodexJob `json:"job"`
	Previous string         `json:"previous"`
	MAC      string         `json:"mac"`
}

var validID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

func failure(code string) error { return errors.New(code) }

func New(cfg Config) (*Bridge, error) {
	if !validID.MatchString(cfg.DeviceID) || cfg.StateDir == "" {
		return nil, failure("invalid_config")
	}
	if err := os.MkdirAll(cfg.StateDir, 0700); err != nil {
		return nil, failure("storage_unavailable")
	}
	if err := privateDirectory(cfg.StateDir); err != nil {
		return nil, failure("storage_unavailable")
	}
	l := flock.New(filepath.Join(cfg.StateDir, "codex-jobs.lock"))
	if ok, err := l.TryLock(); err != nil || !ok {
		return nil, failure("state_locked")
	}
	b := &Bridge{deviceID: cfg.DeviceID, lock: l, jobs: map[string]model.CodexJob{}, requests: map[string]string{}}
	if err := b.open(cfg.StateDir); err != nil {
		if b.file != nil {
			b.file.Close()
		}
		l.Unlock()
		return nil, err
	}
	if err := b.openRuntime(cfg); err != nil {
		b.file.Close()
		l.Unlock()
		return nil, err
	}
	return b, nil
}

func (b *Bridge) open(dir string) error {
	ledgerPath, keyPath := filepath.Join(dir, "codex-jobs.jsonl"), filepath.Join(dir, "codex-jobs.key")
	key, err := os.ReadFile(keyPath)
	if errors.Is(err, os.ErrNotExist) {
		if info, e := os.Stat(ledgerPath); e == nil && info.Size() > 0 {
			return failure("storage_corrupt")
		} else if e != nil && !errors.Is(e, os.ErrNotExist) {
			return failure("storage_unavailable")
		}
		key = make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return failure("storage_unavailable")
		}
		f, e := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return failure("storage_unavailable")
		}
		_, e = f.Write(key)
		if e == nil {
			e = f.Sync()
		}
		closeErr := f.Close()
		if e != nil || closeErr != nil {
			return failure("storage_unavailable")
		}
	} else if err != nil {
		return failure("storage_unavailable")
	}
	if len(key) != 32 {
		return failure("storage_corrupt")
	}
	b.key = key
	b.file, err = os.OpenFile(ledgerPath, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return failure("storage_unavailable")
	}
	s := bufio.NewScanner(b.file)
	s.Buffer(make([]byte, 4096), 16384)
	for s.Scan() {
		var r record
		if json.Unmarshal(s.Bytes(), &r) != nil || r.Previous != b.chain || !hmac.Equal([]byte(r.MAC), []byte(b.sign(r.Job, r.Previous))) || !b.validStored(r.Job) {
			return failure("storage_corrupt")
		}
		if oldID, ok := b.requests[r.Job.RequestID]; ok {
			old := b.jobs[oldID]
			if oldID != r.Job.JobID || old.RequestHash != r.Job.RequestHash || old.PromptHash != r.Job.PromptHash || old.State != r.Job.State || old.Code != r.Job.Code || !old.CreatedAt.Equal(r.Job.CreatedAt) || (old.CancelRequested && !r.Job.CancelRequested) {
				return failure("storage_corrupt")
			}
		} else if _, ok := b.jobs[r.Job.JobID]; ok {
			return failure("storage_corrupt")
		}
		b.jobs[r.Job.JobID], b.requests[r.Job.RequestID], b.chain = r.Job, r.Job.JobID, r.MAC
	}
	if s.Err() != nil {
		return failure("storage_corrupt")
	}
	// An incomplete final append must never become a replayable new request.
	if info, err := b.file.Stat(); err != nil {
		return failure("storage_unavailable")
	} else if info.Size() > 0 {
		last := make([]byte, 1)
		if _, err = b.file.ReadAt(last, info.Size()-1); err != nil || last[0] != '\n' {
			return failure("storage_corrupt")
		}
	}
	if err := b.file.Sync(); err != nil {
		return failure("storage_unavailable")
	}
	if err := syncDirectory(dir); err != nil {
		return failure("storage_unavailable")
	}
	return nil
}
func (b *Bridge) validStored(j model.CodexJob) bool {
	return j.DeviceID == b.deviceID && validID.MatchString(j.JobID) && validID.MatchString(j.RequestID) && (j.State == "blocked" || j.State == "rejected") && len(j.RequestHash) == 64 && len(j.PromptHash) == 64 && !j.CreatedAt.IsZero() && !j.UpdatedAt.Before(j.CreatedAt)
}
func (b *Bridge) hash(domain string, value []byte) string {
	h := hmac.New(sha256.New, b.key)
	h.Write([]byte(domain + "\x00"))
	h.Write(value)
	return hex.EncodeToString(h.Sum(nil))
}
func (b *Bridge) sign(j model.CodexJob, previous string) string {
	value, _ := json.Marshal(j)
	return b.hash("record:"+previous, value)
}
func (b *Bridge) save(j model.CodexJob) error {
	r := record{Job: j, Previous: b.chain, MAC: b.sign(j, b.chain)}
	data, _ := json.Marshal(r)
	data = append(data, '\n')
	n, err := b.file.Write(data)
	if err != nil || n != len(data) {
		b.broken = true
		return failure("storage_unavailable")
	}
	if err = b.file.Sync(); err != nil {
		b.broken = true
		return failure("storage_unavailable")
	}
	b.jobs[j.JobID], b.requests[j.RequestID], b.chain = j, j.JobID, r.MAC
	return nil
}
func (b *Bridge) healthy() error {
	if b.closed {
		return failure("closed")
	}
	if b.broken {
		return failure("storage_unavailable")
	}
	return nil
}
func (b *Bridge) Submit(ctx context.Context, r model.CodexSubmit) (model.CodexJob, error) {
	if r.ProfileID != "" {
		return b.submitRuntime(ctx, r)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.healthy(); err != nil {
		return model.CodexJob{}, err
	}
	if r.DeviceID != b.deviceID {
		return model.CodexJob{}, failure("wrong_device")
	}
	if !validID.MatchString(r.RequestID) {
		return model.CodexJob{}, failure("invalid_request_id")
	}
	value, _ := json.Marshal(r)
	hash := b.hash("request", value)
	if _, ok := b.runtime.requests[r.RequestID]; ok {
		return model.CodexJob{}, failure("request_id_conflict")
	}
	if id, ok := b.requests[r.RequestID]; ok {
		j := b.jobs[id]
		if j.RequestHash != hash {
			return model.CodexJob{}, failure("request_id_conflict")
		}
		return j, nil
	}
	if err := ctx.Err(); err != nil {
		return model.CodexJob{}, failure("cancelled")
	}
	state, code, message := "blocked", "official_cu_acceptance_required", "Official native Computer Use execution is not enabled: image delivery, delegated action, local human approval, and interruption acceptance are pending."
	if reason := validate(r); reason != "" {
		state, code, message = "rejected", reason, "The delegation request is invalid; no Codex thread or desktop action was started."
	}
	id := make([]byte, 24)
	if _, err := rand.Read(id); err != nil {
		return model.CodexJob{}, failure("storage_unavailable")
	}
	now := time.Now().UTC()
	j := model.CodexJob{JobID: "codex-" + hex.EncodeToString(id), DeviceID: b.deviceID, RequestID: r.RequestID, State: state, Code: code, Message: message, CreatedAt: now, UpdatedAt: now, PromptHash: b.hash("prompt", []byte(r.Prompt)), RequestHash: hash}
	if err := b.save(j); err != nil {
		return model.CodexJob{}, err
	}
	return j, nil
}
func validate(r model.CodexSubmit) string {
	if len(r.Prompt) > 65536 || strings.TrimSpace(r.Prompt) == "" {
		return "invalid_prompt"
	}
	if len(r.AllowedApps) == 0 || len(r.AllowedApps) > 64 {
		return "invalid_allowed_apps"
	}
	for _, app := range r.AllowedApps {
		if strings.TrimSpace(app) == "" || len(app) > 512 || strings.ContainsAny(app, "\x00\r\n*") {
			return "invalid_allowed_apps"
		}
	}
	if len(r.WorkingDirs) == 0 || len(r.WorkingDirs) > 64 {
		return "invalid_working_dirs"
	}
	for _, dir := range r.WorkingDirs {
		if !filepath.IsAbs(dir) || len(dir) > 4096 || strings.ContainsAny(dir, "\x00\r\n*") {
			return "invalid_working_dirs"
		}
	}
	return ""
}
func (b *Bridge) Job(deviceID, jobID string) (model.CodexJob, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.job(deviceID, jobID)
}
func (b *Bridge) job(deviceID, jobID string) (model.CodexJob, error) {
	if err := b.healthy(); err != nil {
		return model.CodexJob{}, err
	}
	if deviceID != b.deviceID {
		return model.CodexJob{}, failure("wrong_device")
	}
	if j, ok := b.runtime.jobs[jobID]; ok {
		return cloneJob(j.entry.Job), nil
	}
	j, ok := b.jobs[jobID]
	if !ok {
		return model.CodexJob{}, failure("job_not_found")
	}
	return j, nil
}
func (b *Bridge) Cancel(deviceID, jobID string) (model.CodexJob, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	j, err := b.job(deviceID, jobID)
	if err != nil {
		return model.CodexJob{}, err
	}
	if j.CancelRequested {
		return j, nil
	}
	if _, ok := b.runtime.jobs[jobID]; ok {
		return b.cancelRuntimeLocked(jobID)
	}
	j.CancelRequested, j.UpdatedAt = true, time.Now().UTC()
	// Blocked/rejected are terminal. Cancellation records intent, not a fictitious
	// native interruption, and never contacts a foreign or user-created thread.
	if err = b.save(j); err != nil {
		return model.CodexJob{}, err
	}
	return j, nil
}
func DisabledCapabilities() model.CodexCapabilities {
	return model.CodexCapabilities{Base: model.Capability{State: "not_probed", Code: "driver_not_probed"}, CodexConnection: model.Capability{State: "not_probed", Code: "codex_not_probed"}, OfficialComputerUse: model.Capability{State: "not_verified", Code: "official_cu_acceptance_required"}, CodexDelegation: model.Capability{State: "blocked", Code: "official_cu_acceptance_required"}}
}
func (b *Bridge) Capabilities() model.CodexCapabilities { return b.runtimeCapabilities() }
func (b *Bridge) Close() error {
	b.closeRuntime()
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	if err := b.file.Close(); err != nil {
		b.lock.Unlock()
		return failure("storage_unavailable")
	}
	if err := b.lock.Unlock(); err != nil {
		return failure("storage_unavailable")
	}
	return nil
}
