// Package engine coordinates the sole writer for a device and its durable action ledger.
package engine

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/model"
	"github.com/gofrs/flock"
)

// Config configures conservative device-local execution limits.
type Config struct {
	DeviceID, Name, StateDir                     string
	IdleThreshold, ObservationTTL, ActionTimeout time.Duration
	InitialPaused                                bool // Used only when creating a new ledger, never on replay.
}

// Error contains only a stable public code; native driver and filesystem errors
// are never attached, because they can include private content.
type Error struct{ Code string }

func (e *Error) Error() string { return e.Code }
func fail(code string) error   { return &Error{Code: code} }

var validID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var validKind = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

type Engine struct {
	cfg                    Config
	driver                 model.Driver
	monitor                model.Monitor
	mu                     sync.Mutex
	gate                   chan struct{}
	stop                   chan struct{}
	closed, paused, broken bool
	lock                   *flock.Flock
	file                   *os.File
	key                    []byte
	chain                  string
	nonce                  string
	receipts               map[string]model.Receipt
	cancelled              map[string]bool
	observations           map[string]model.Observation
	observationGeneration  uint64
	latest                 map[model.Target]string
	activeTask             string
	activeCancel           context.CancelFunc
	activeDone             chan struct{}
	externalPending        string
	progress               ExecutionProgress
	syncFile               func(*os.File) error
}

func New(cfg Config, driver model.Driver, monitor model.Monitor) (*Engine, error) {
	if !validID.MatchString(cfg.DeviceID) || cfg.StateDir == "" || driver == nil || monitor == nil {
		return nil, fail("invalid_config")
	}
	if cfg.IdleThreshold == 0 {
		cfg.IdleThreshold = 60 * time.Second
	}
	if cfg.ObservationTTL == 0 {
		cfg.ObservationTTL = 30 * time.Second
	}
	if cfg.ActionTimeout == 0 {
		cfg.ActionTimeout = 30 * time.Second
	}
	if cfg.IdleThreshold < 0 || cfg.ObservationTTL < 0 || cfg.ActionTimeout < 0 {
		return nil, fail("invalid_config")
	}
	if err := os.MkdirAll(cfg.StateDir, 0700); err != nil {
		return nil, fail("storage_unavailable")
	}
	if err := privateDirectory(cfg.StateDir); err != nil {
		return nil, fail("storage_unavailable")
	}
	l := flock.New(filepath.Join(cfg.StateDir, "engine.lock"))
	ok, err := l.TryLock()
	if err != nil || !ok {
		return nil, fail("state_locked")
	}
	e := &Engine{cfg: cfg, driver: driver, monitor: monitor, lock: l, gate: make(chan struct{}, 1), stop: make(chan struct{}), receipts: map[string]model.Receipt{}, cancelled: map[string]bool{}, observations: map[string]model.Observation{}, latest: map[model.Target]string{}, syncFile: func(f *os.File) error { return f.Sync() }}
	e.gate <- struct{}{}
	e.nonce = randomID()
	if e.nonce == "" {
		l.Unlock()
		return nil, fail("storage_unavailable")
	}
	if err = e.openLedger(); err != nil {
		if e.file != nil {
			e.file.Close()
		}
		l.Unlock()
		return nil, err
	}
	return e, nil
}
func randomID() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}
func actionKey(task, id string) string { return task + "\x00" + id }
func (e *Engine) healthyLocked() error {
	if e.closed {
		return fail("closed")
	}
	if e.broken {
		return fail("storage_unavailable")
	}
	return nil
}
func (e *Engine) acquire(ctx context.Context) error {
	e.mu.Lock()
	blocked := e.externalPending != ""
	e.mu.Unlock()
	if blocked {
		return fail("external_execution_unresolved")
	}
	select {
	case <-e.stop:
		return fail("closed")
	default:
	}
	select {
	case <-e.stop:
		return fail("closed")
	case <-ctx.Done():
		return fail("cancelled")
	case <-e.gate:
		e.mu.Lock()
		blocked = e.externalPending != ""
		e.mu.Unlock()
		if blocked {
			e.unlock()
			return fail("external_execution_unresolved")
		}
		return nil
	}
}
func (e *Engine) unlock() { e.gate <- struct{}{} }
func (e *Engine) Device() model.Device {
	e.mu.Lock()
	paused := e.paused
	name := e.cfg.Name
	e.mu.Unlock()
	nonce := randomID()
	if nonce == "" {
		nonce = e.nonce
	}
	return model.Device{ID: e.cfg.DeviceID, Name: name, OS: runtime.GOOS, Version: model.Version, Paused: paused, Activity: e.monitor.Snapshot(), Nonce: nonce}
}
func (e *Engine) Apps(ctx context.Context) (json.RawMessage, error) {
	if err := e.acquire(ctx); err != nil {
		return nil, err
	}
	defer e.unlock()
	e.mu.Lock()
	err := e.healthyLocked()
	e.mu.Unlock()
	if err != nil {
		return nil, err
	}
	apps, err := e.driver.Apps(ctx)
	if err != nil {
		return nil, fail("driver_unavailable")
	}
	return apps, nil
}
func validTarget(t model.Target) bool {
	if t.PID <= 0 || len(t.WindowID) == 0 || len(t.WindowID) > 256 {
		return false
	}
	for _, r := range t.WindowID {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}
func cloneView(v model.View) model.View {
	v.Elements = append([]string(nil), v.Elements...)
	v.Images = append([]model.Image(nil), v.Images...)
	return v
}
func (e *Engine) Observe(ctx context.Context, t model.Target) (model.Observation, error) {
	if !validTarget(t) {
		return model.Observation{}, fail("invalid_target")
	}
	if err := e.acquire(ctx); err != nil {
		return model.Observation{}, err
	}
	defer e.unlock()
	e.mu.Lock()
	generation := e.observationGeneration
	err := e.healthyLocked()
	if err == nil && e.paused {
		err = fail("paused")
	}
	e.mu.Unlock()
	if err != nil {
		return model.Observation{}, err
	}
	v, err := e.driver.Observe(ctx, t)
	if err != nil {
		return model.Observation{}, fail("observation_failed")
	}
	if v.Target != t {
		return model.Observation{}, fail("wrong_target")
	}
	if ctx.Err() != nil {
		return model.Observation{}, fail("cancelled")
	}
	now := time.Now().UTC()
	o := model.Observation{ID: randomID(), DeviceID: e.cfg.DeviceID, CreatedAt: now, ExpiresAt: now.Add(e.cfg.ObservationTTL), View: cloneView(v)}
	if o.ID == "" {
		return model.Observation{}, fail("observation_failed")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err = e.healthyLocked(); err != nil {
		return model.Observation{}, err
	}
	if e.paused {
		return model.Observation{}, fail("paused")
	}
	if generation != e.observationGeneration {
		return model.Observation{}, fail("stale_observation")
	}
	if old := e.latest[t]; old != "" {
		delete(e.observations, old)
	}
	e.observations[o.ID] = o
	e.latest[t] = o.ID
	o.View = cloneView(o.View)
	return o, nil
}

func (e *Engine) prepare(a model.Action) (model.Action, string, error) {
	if a.DeviceID != e.cfg.DeviceID {
		return a, "", fail("wrong_device")
	}
	if !validID.MatchString(a.TaskID) || !validID.MatchString(a.ActionID) || !validID.MatchString(a.ObservationID) {
		return a, "", fail("invalid_id")
	}
	if !validTarget(a.Target) {
		return a, "", fail("invalid_target")
	}
	if a.Mode == "" {
		a.Mode = "auto"
	}
	if a.Mode != "auto" && a.Mode != "background" && a.Mode != "foreground" || !validKind.MatchString(a.Kind) || len(a.Args) > 1024*1024 || len(a.Expect) > 4096 {
		return a, "", fail("invalid_action")
	}
	// Decode then encode to canonicalize object ordering and insignificant whitespace.
	var args map[string]any
	dec := json.NewDecoder(bytes.NewReader(a.Args))
	dec.UseNumber()
	if err := dec.Decode(&args); err != nil || args == nil {
		return a, "", fail("invalid_action")
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return a, "", fail("invalid_action")
	}
	a.Args, _ = json.Marshal(args)
	b, err := json.Marshal(a)
	if err != nil {
		return a, "", fail("invalid_action")
	}
	mac := hmac.New(sha256.New, e.key)
	mac.Write(b)
	return a, hex.EncodeToString(mac.Sum(nil)), nil
}
func elementAllowed(a model.Action, v model.View) bool {
	var args map[string]json.RawMessage
	if json.Unmarshal(a.Args, &args) != nil {
		return false
	}
	raw, ok := args["element_token"]
	if !ok {
		return true
	}
	var token string
	if json.Unmarshal(raw, &token) != nil || token == "" {
		return false
	}
	for _, element := range v.Elements {
		if element == token {
			return true
		}
	}
	return false
}
func busy(a model.Activity, idle time.Duration) bool {
	return !a.Available || a.IdleMillis < 0 || time.Duration(a.IdleMillis)*time.Millisecond < idle
}
func inTarget(a model.Activity, t model.Target) bool {
	return a.ForegroundPID == t.PID || (a.ForegroundWindow != "" && a.ForegroundWindow == t.WindowID)
}
func (e *Engine) mode(a model.Action, v model.View, activity model.Activity) (string, string) {
	if !activity.Available || activity.Locked {
		return "", "human_unavailable"
	}
	isBusy := busy(activity, e.cfg.IdleThreshold)
	if isBusy && inTarget(activity, a.Target) {
		return "", "human_busy"
	}
	if a.Mode == "foreground" {
		if isBusy {
			return "", "human_busy"
		}
		return "foreground", ""
	}
	supported := e.driver.SupportsBackground(a, cloneView(v))
	if supported {
		return "background", ""
	}
	if a.Mode == "background" {
		return "", "background_unsupported"
	}
	if isBusy {
		return "", "human_busy"
	}
	return "foreground", ""
}
func (e *Engine) revoked(initial, current model.Activity, t model.Target, mode string) bool {
	if !current.Available || current.Locked || current.IdleMillis < 0 {
		return true
	}
	if mode == "foreground" {
		return initial.Generation != current.Generation || busy(current, e.cfg.IdleThreshold)
	}
	return busy(current, e.cfg.IdleThreshold) && inTarget(current, t)
}
func receipt(a model.Action, hash, state, mode, code string) model.Receipt {
	return model.Receipt{DeviceID: a.DeviceID, TaskID: a.TaskID, ActionID: a.ActionID, Hash: hash, State: state, Mode: mode, Code: code, UpdatedAt: time.Now().UTC()}
}
func (e *Engine) saveReceiptLocked(r model.Receipt) error {
	stored := r
	// Persist stable metadata only. Public explanations are reconstructed from
	// the reviewed code table after replay, never trusted from native output.
	stored.Message = ""
	if err := e.appendEvent(ledgerEvent{Type: "receipt", Receipt: &storedReceipt{Receipt: stored, Hash: r.Hash}}); err != nil {
		return err
	}
	e.receipts[actionKey(r.TaskID, r.ActionID)] = r
	return nil
}
func (e *Engine) Execute(ctx context.Context, input model.Action) (model.Receipt, error) {
	queuedAt := time.Now()
	a, hash, err := e.prepare(input)
	if err != nil {
		return model.Receipt{}, err
	}
	// Cached final receipts do not wait for an unresponsive native operation.
	// Concurrent retries of an active request wait for its durable final receipt.
	for {
		e.mu.Lock()
		err = e.healthyLocked()
		r, exists := e.receipts[actionKey(a.TaskID, a.ActionID)]
		activeDone := e.activeDone
		e.mu.Unlock()
		if err != nil {
			return model.Receipt{}, err
		}
		if !exists {
			break
		}
		if r.Hash != hash {
			return model.Receipt{}, fail("action_conflict")
		}
		if r.State != "dispatching" {
			return r, nil
		}
		select {
		case <-ctx.Done():
			return model.Receipt{}, fail("cancelled")
		case <-activeDone:
		}
	}
	if err = e.acquire(ctx); err != nil {
		return model.Receipt{}, err
	}
	transferred := false
	defer func() {
		if !transferred {
			e.unlock()
		}
	}()
	e.mu.Lock()
	if err = e.healthyLocked(); err != nil {
		e.mu.Unlock()
		return model.Receipt{}, err
	}
	if r, ok := e.receipts[actionKey(a.TaskID, a.ActionID)]; ok {
		e.mu.Unlock()
		if r.Hash != hash {
			return model.Receipt{}, fail("action_conflict")
		}
		return r, nil
	}
	rejection := ""
	state := "needs_user"
	if e.cancelled[a.TaskID] {
		state = "cancelled"
		rejection = "task_cancelled"
	} else if e.paused {
		rejection = "paused"
	}
	if rejection != "" {
		r := receipt(a, hash, state, "", rejection)
		err = e.saveReceiptLocked(r)
		e.mu.Unlock()
		return r, err
	}
	o, ok := e.observations[a.ObservationID]
	if !ok || e.latest[o.View.Target] != o.ID || !time.Now().Before(o.ExpiresAt) {
		e.mu.Unlock()
		return model.Receipt{}, fail("stale_observation")
	}
	if o.DeviceID != a.DeviceID || o.View.Target != a.Target {
		e.mu.Unlock()
		return model.Receipt{}, fail("wrong_target")
	}
	if !elementAllowed(a, o.View) {
		e.mu.Unlock()
		return model.Receipt{}, fail("invalid_element")
	}
	e.mu.Unlock()
	initial := e.monitor.Snapshot()
	mode, rejection := e.mode(a, o.View, initial)
	if rejection == "" {
		if e.driver.Check(ctx, cloneView(o.View)) != nil {
			rejection = "target_changed"
		} else if e.revoked(initial, e.monitor.Snapshot(), a.Target, mode) {
			rejection = "human_changed"
		}
	}
	e.mu.Lock()
	if err = e.healthyLocked(); err != nil {
		e.mu.Unlock()
		return model.Receipt{}, err
	}
	if e.cancelled[a.TaskID] {
		state = "cancelled"
		rejection = "task_cancelled"
	} else if e.paused {
		rejection = "paused"
	}
	if ctx.Err() != nil {
		state = "cancelled"
		rejection = "request_cancelled"
	}
	if rejection != "" {
		if rejection == "background_unsupported" {
			state = "unsupported"
		}
		r := receipt(a, hash, state, mode, rejection)
		err = e.saveReceiptLocked(r)
		e.mu.Unlock()
		return r, err
	}
	// A pause can invalidate every observation while the read-only driver
	// check runs without mu held. Resuming does not restore those observations.
	current, stillPresent := e.observations[a.ObservationID]
	if !stillPresent || current.ID != o.ID || e.latest[a.Target] != o.ID || !time.Now().Before(o.ExpiresAt) {
		e.mu.Unlock()
		return model.Receipt{}, fail("stale_observation")
	}
	r := receipt(a, hash, "dispatching", mode, "")
	if err = e.saveReceiptLocked(r); err != nil {
		e.mu.Unlock()
		return model.Receipt{}, err
	}
	delete(e.observations, o.ID)
	delete(e.latest, o.View.Target)
	runCtx, cancel := context.WithTimeout(ctx, e.cfg.ActionTimeout)
	e.activeCancel = cancel
	e.activeTask = a.TaskID
	e.progress = ExecutionProgress{TaskID: a.TaskID, ActionID: a.ActionID, NativePending: true, QueueMillis: time.Since(queuedAt).Milliseconds()}
	e.activeDone = make(chan struct{})
	done := make(chan runResult, 1)
	activeDone := e.activeDone
	e.mu.Unlock()
	transferred = true
	go e.run(runCtx, cancel, a, cloneView(o.View), o.ExpiresAt, initial, r, done, activeDone)
	result := <-done
	return result.receipt, result.err
}

type runResult struct {
	receipt model.Receipt
	err     error
}

func (e *Engine) run(ctx context.Context, cancel context.CancelFunc, a model.Action, v model.View, expiresAt time.Time, initial model.Activity, r model.Receipt, done chan<- runResult, activeDone chan struct{}) {
	defer cancel()
	started := time.Now()
	output := make(chan model.Outcome, 1)
	go func() {
		// Journal sync can outlast the observation or allow human activity to
		// change. Recheck both immediately before invoking the native driver.
		if ctx.Err() != nil || !time.Now().Before(expiresAt) || e.revoked(initial, e.monitor.Snapshot(), a.Target, r.Mode) {
			output <- model.Outcome{State: "unknown"}
			return
		}
		output <- e.driver.Execute(ctx, a, v, r.Mode)
	}()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	received := false
	state := "unknown"
	reason := "execution_uncertain"
	explanation := ""
loop:
	for {
		select {
		case outcome := <-output:
			received = true
			if ctx.Err() == nil && !e.revoked(initial, e.monitor.Snapshot(), a.Target, r.Mode) {
				switch outcome.State {
				case "succeeded":
					state = "succeeded"
					reason = "verified"
				case "unsupported":
					state = "unsupported"
					reason = "driver_unsupported"
				case "needs_user":
					state = "needs_user"
					reason = "driver_needs_user"
					// Only reviewed, content-free codes cross the driver boundary.
					// Never persist the driver's arbitrary Message or error payload.
					if hint := publicDriverHints[outcome.Code]; hint != "" {
						reason, explanation = outcome.Code, hint
					}
				case "failed":
					state = "failed"
					reason = "driver_failed"
				}
				if evidenceState, ok := publicDriverCauses[outcome.Code]; ok {
					reason = outcome.Code
					// Evidence codes never promote an outcome. Contradictory
					// success claims are bounded by the weaker driver evidence.
					if state == "succeeded" && evidenceState != "succeeded" {
						state = evidenceState
					}
				}
			}
			break loop
		case <-ctx.Done():
			break loop
		case <-ticker.C:
			if e.revoked(initial, e.monitor.Snapshot(), a.Target, r.Mode) {
				cancel()
			}
		}
	}
	cleanupDone, clean := e.release()
	if !clean {
		state = "unknown"
		reason = "release_uncertain"
	}
	e.mu.Lock()
	if ctx.Err() != nil || e.revoked(initial, e.monitor.Snapshot(), a.Target, r.Mode) {
		state = "unknown"
		reason = "execution_uncertain"
	}
	r.State = state
	r.Code = reason
	if state == "needs_user" {
		r.Message = explanation
	}
	r.UpdatedAt = time.Now().UTC()
	if state == "unknown" {
		e.observations = map[string]model.Observation{}
		e.latest = map[model.Target]string{}
	}
	err := e.saveReceiptLocked(r)
	if err != nil {
		r.State = "unknown"
		r.Code = "storage_unavailable"
		r.Message = ""
	}
	e.progress.State, e.progress.Code = r.State, r.Code
	e.activeCancel = nil
	e.activeTask = ""
	e.activeDone = nil
	e.mu.Unlock()
	done <- runResult{receipt: r, err: err}
	close(activeDone)
	// Even an uncooperative driver retains the sole-writer gate. Cancellation
	// returns an unknown receipt promptly, but no subsequent native call can race it.
	if !received {
		<-output
		later, _ := e.release()
		<-later
	}
	<-cleanupDone
	e.mu.Lock()
	e.progress.NativePending = false
	e.progress.ExecutionMillis = time.Since(started).Milliseconds()
	e.mu.Unlock()
	e.unlock()
}

// Reviewed content-free causes only; arbitrary driver codes/messages stay private.
// The value is the strongest state each piece of evidence can support.
var publicDriverCauses = map[string]string{
	"effect_confirmed":         "succeeded",
	"effect_none":              "failed",
	"effect_partial":           "unknown",
	"effect_refused":           "needs_user",
	"effect_suspected_noop":    "unknown",
	"effect_unknown":           "unknown",
	"postcondition_verified":   "succeeded",
	"postcondition_unverified": "unknown",
	"postcondition_invalid":    "unknown",
}

var publicDriverHints = map[string]string{
	"capture_binding_unavailable": "Use an observed accessibility element or another supported action; this coordinate action cannot bind to the screenshot.",
	"native_text_required":        "macOS set_value requires an enabled native text field and nonnumeric text; this target needs another supported route or user input.",
	"use_set_value":               "For multi-character macOS element text, observe again and use set_value on a supported native text field.",
}

func (e *Engine) release() (<-chan struct{}, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	done := make(chan struct{})
	result := make(chan error, 1)
	go func() { defer close(done); result <- e.driver.Release(ctx) }()
	defer cancel()
	select {
	case err := <-result:
		return done, err == nil
	case <-ctx.Done():
		return done, false
	}
}
func (e *Engine) Task(taskID string) ([]model.Receipt, error) {
	if !validID.MatchString(taskID) {
		return nil, fail("invalid_id")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.healthyLocked(); err != nil {
		return nil, err
	}
	out := []model.Receipt{}
	for _, r := range e.receipts {
		if r.TaskID == taskID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].ActionID < out[j].ActionID
		}
		return out[i].UpdatedAt.Before(out[j].UpdatedAt)
	})
	return out, nil
}
func (e *Engine) Cancel(taskID string) error {
	if !validID.MatchString(taskID) {
		return fail("invalid_id")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.healthyLocked(); err != nil {
		return err
	}
	if !e.cancelled[taskID] {
		if err := e.appendEvent(ledgerEvent{Type: "cancel", TaskID: taskID}); err != nil {
			return err
		}
		e.cancelled[taskID] = true
	}
	if e.activeTask == taskID && e.activeCancel != nil {
		e.activeCancel()
	}
	return nil
}
func (e *Engine) Pause(paused bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.healthyLocked(); err != nil {
		return err
	}
	if e.paused != paused {
		if err := e.appendEvent(ledgerEvent{Type: "pause", Paused: &paused}); err != nil {
			return err
		}
		e.paused = paused
	}
	if paused {
		e.observationGeneration++
		e.observations = map[string]model.Observation{}
		e.latest = map[model.Target]string{}
		if e.activeCancel != nil {
			e.activeCancel()
		}
	}
	return nil
}
func (e *Engine) Close() error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	close(e.stop)
	if e.activeCancel != nil {
		e.activeCancel()
	}
	done := e.activeDone
	e.mu.Unlock()
	if done != nil {
		<-done
	}
	// Retain the OS process lock until every native operation actually exits,
	// including an operation that ignored cancellation and returned late.
	<-e.gate
	e.mu.Lock()
	defer e.mu.Unlock()
	err := e.file.Close()
	if closer, ok := e.driver.(io.Closer); ok {
		_ = closer.Close()
	}
	lockErr := e.lock.Unlock()
	if err != nil || lockErr != nil {
		return fail("storage_unavailable")
	}
	return nil
}
