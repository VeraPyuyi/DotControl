package engine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/model"
)

type testMonitor struct {
	mu       sync.Mutex
	activity model.Activity
}

func (m *testMonitor) Snapshot() model.Activity { m.mu.Lock(); defer m.mu.Unlock(); return m.activity }
func (m *testMonitor) set(a model.Activity)     { m.mu.Lock(); m.activity = a; m.mu.Unlock() }

type testDriver struct {
	calls      atomic.Int32
	releases   atomic.Int32
	background bool
	execute    func(context.Context, model.Action, model.View, string) model.Outcome
	check      func(context.Context, model.View) error
	observe    func(context.Context, model.Target) (model.View, error)
	releaseFn  func(context.Context) error
}

func (d *testDriver) Apps(context.Context) (json.RawMessage, error) {
	return json.RawMessage(`[]`), nil
}
func (d *testDriver) Observe(ctx context.Context, t model.Target) (model.View, error) {
	if d.observe != nil {
		return d.observe(ctx, t)
	}
	return model.View{Target: t, Fingerprint: "fp", Elements: []string{"e1"}, Text: "private observation"}, nil
}
func (d *testDriver) SupportsBackground(model.Action, model.View) bool { return d.background }
func (d *testDriver) Check(ctx context.Context, v model.View) error {
	if d.check != nil {
		return d.check(ctx, v)
	}
	return nil
}
func (d *testDriver) Execute(ctx context.Context, a model.Action, v model.View, m string) model.Outcome {
	d.calls.Add(1)
	if d.execute != nil {
		return d.execute(ctx, a, v, m)
	}
	return model.Outcome{State: "succeeded", Code: "PRIVATE_DRIVER_PAYLOAD", Message: "PRIVATE_DRIVER_PAYLOAD"}
}
func (d *testDriver) Release(ctx context.Context) error {
	d.releases.Add(1)
	if d.releaseFn != nil {
		return d.releaseFn(ctx)
	}
	return nil
}

var target = model.Target{PID: 100, WindowID: "win-100"}

func idle() model.Activity {
	return model.Activity{Available: true, IdleMillis: 30000, Generation: 1, ForegroundPID: 200, ForegroundWindow: "other"}
}
func setup(t *testing.T) (*Engine, *testDriver, *testMonitor, Config) {
	t.Helper()
	d := &testDriver{}
	m := &testMonitor{activity: idle()}
	cfg := Config{DeviceID: "device-1", Name: "Test", StateDir: t.TempDir(), IdleThreshold: time.Second, ObservationTTL: time.Minute, ActionTimeout: time.Second}
	e, err := New(cfg, d, m)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	return e, d, m, cfg
}
func action(t *testing.T, e *Engine, id string) model.Action {
	t.Helper()
	o, err := e.Observe(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	return model.Action{DeviceID: "device-1", TaskID: "task-1", ActionID: id, ObservationID: o.ID, Target: target, Kind: "click", Args: json.RawMessage(`{"element_token":"e1"}`), Mode: "auto"}
}
func execute(t *testing.T, e *Engine, a model.Action) model.Receipt {
	t.Helper()
	r, err := e.Execute(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func TestIdempotentDispatchAndPrivatePersistence(t *testing.T) {
	e, d, _, cfg := setup(t)
	a := action(t, e, "a1")
	a.Args = json.RawMessage(`{"text":"HIGHLY_PRIVATE_TYPED_TEXT","element_token":"e1"}`)
	r := execute(t, e, a)
	if r.State != "succeeded" {
		t.Fatalf("state=%s", r.State)
	}
	r2 := execute(t, e, a)
	if r2.State != r.State || d.calls.Load() != 1 {
		t.Fatal("retry dispatched twice")
	}
	a.Args = json.RawMessage(`{"element_token":"e1","text":"HIGHLY_PRIVATE_TYPED_TEXT"}`)
	execute(t, e, a)
	if d.calls.Load() != 1 {
		t.Fatal("canonical JSON retry repeated")
	}
	a.Args = json.RawMessage(`{"element_token":"e1","text":"different"}`)
	if _, err := e.Execute(context.Background(), a); code(err) != "action_conflict" {
		t.Fatalf("conflict=%v", err)
	}
	receipts, err := e.Task("task-1")
	if err != nil || len(receipts) != 1 {
		t.Fatalf("receipts=%v %v", receipts, err)
	}
	b, err := os.ReadFile(filepath.Join(cfg.StateDir, "ledger.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"HIGHLY_PRIVATE_TYPED_TEXT", "PRIVATE_DRIVER_PAYLOAD", "private observation"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("leaked %s", secret)
		}
	}
	rb, _ := json.Marshal(r)
	if strings.Contains(string(rb), "PRIVATE_DRIVER_PAYLOAD") {
		t.Fatal("driver payload leaked in receipt")
	}
}

func TestDriverBlockerHintsAreAllowlistedAndPrivate(t *testing.T) {
	for _, driverCode := range []string{"use_set_value", "native_text_required", "capture_binding_unavailable", "PRIVATE_UNKNOWN_CODE"} {
		t.Run(driverCode, func(t *testing.T) {
			e, d, m, cfg := setup(t)
			d.execute = func(context.Context, model.Action, model.View, string) model.Outcome {
				return model.Outcome{State: "needs_user", Code: driverCode, Message: "PRIVATE_NATIVE_DETAILS"}
			}
			a := action(t, e, "hint-1")
			r := execute(t, e, a)
			if r.State != "needs_user" {
				t.Fatalf("state=%s", r.State)
			}
			if expected := publicDriverHints[driverCode]; expected != "" {
				if r.Code != driverCode || r.Message != expected {
					t.Fatalf("missing public blocker hint: %+v", r)
				}
			} else if r.Code != "driver_needs_user" || r.Message != "" {
				t.Fatalf("arbitrary native hint leaked: %+v", r)
			}
			journal, err := os.ReadFile(filepath.Join(cfg.StateDir, "ledger.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(journal), "PRIVATE_") {
				t.Fatal("private driver details persisted")
			}
			if r.Message != "" && strings.Contains(string(journal), r.Message) {
				t.Fatal("explanations should be reconstructed, not journaled")
			}
			if err := e.Close(); err != nil {
				t.Fatal(err)
			}
			restartedDriver := &testDriver{}
			restarted, err := New(cfg, restartedDriver, m)
			if err != nil {
				t.Fatalf("blocker receipt prevented restart: %v", err)
			}
			defer restarted.Close()
			retry := execute(t, restarted, a)
			if retry.State != r.State || retry.Code != r.Code || retry.Message != r.Message || restartedDriver.calls.Load() != 0 {
				t.Fatalf("restart lost hint or repeated input: %+v", retry)
			}
		})
	}
}

func TestObservationBindingsAndSingleUse(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*model.Action)
		want   string
	}{
		{"device", func(a *model.Action) { a.DeviceID = "another" }, "wrong_device"},
		{"target", func(a *model.Action) { a.Target.WindowID = "other" }, "wrong_target"},
		{"unknown observation", func(a *model.Action) { a.ObservationID = "missing" }, "stale_observation"},
		{"element", func(a *model.Action) { a.Args = json.RawMessage(`{"element_token":"foreign"}`) }, "invalid_element"},
		{"mode", func(a *model.Action) { a.Mode = "invalid" }, "invalid_action"},
		{"id", func(a *model.Action) { a.TaskID = strings.Repeat("x", 129) }, "invalid_id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, d, _, _ := setup(t)
			a := action(t, e, "a1")
			tc.change(&a)
			if _, err := e.Execute(context.Background(), a); code(err) != tc.want {
				t.Fatalf("got %v want %s", err, tc.want)
			}
			if d.calls.Load() != 0 {
				t.Fatal("invalid action dispatched")
			}
		})
	}
	e, d, _, _ := setup(t)
	a := action(t, e, "a1")
	execute(t, e, a)
	a.ActionID = "a2"
	if _, err := e.Execute(context.Background(), a); code(err) != "stale_observation" {
		t.Fatalf("reused observation: %v", err)
	}
	if d.calls.Load() != 1 {
		t.Fatal("consumed observation dispatched")
	}
}

func TestExpiredAndReplacedObservation(t *testing.T) {
	e, _, _, _ := setup(t)
	a := action(t, e, "a1")
	_, err := e.Observe(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Execute(context.Background(), a); code(err) != "stale_observation" {
		t.Fatalf("old version accepted: %v", err)
	}
	e2, _, _, _ := setup(t)
	e2.cfg.ObservationTTL = time.Millisecond
	a = action(t, e2, "a2")
	time.Sleep(3 * time.Millisecond)
	if _, err = e2.Execute(context.Background(), a); code(err) != "stale_observation" {
		t.Fatalf("expired accepted: %v", err)
	}
}

func TestHumanPolicy(t *testing.T) {
	for _, tc := range []struct {
		name                string
		a                   model.Activity
		background          bool
		mode, want, runMode string
	}{
		{"unknown", model.Activity{}, false, "auto", "needs_user", ""},
		{"locked", model.Activity{Available: true, Locked: true, IdleMillis: 99999}, true, "auto", "needs_user", ""},
		{"busy target", model.Activity{Available: true, ForegroundPID: 100, ForegroundWindow: "win-100"}, true, "auto", "needs_user", ""},
		{"busy unrelated window same process", model.Activity{Available: true, ForegroundPID: 100, ForegroundWindow: "other-window"}, true, "auto", "needs_user", ""},
		{"busy supported background", model.Activity{Available: true, ForegroundPID: 200, ForegroundWindow: "other"}, true, "auto", "succeeded", "background"},
		{"busy unsupported", model.Activity{Available: true, ForegroundPID: 200, ForegroundWindow: "other"}, false, "auto", "needs_user", ""},
		{"idle foreground", idle(), false, "auto", "succeeded", "foreground"},
		{"idle background preferred", idle(), true, "auto", "succeeded", "background"},
		{"explicit idle foreground", idle(), true, "foreground", "succeeded", "foreground"},
		{"explicit busy foreground", model.Activity{Available: true, ForegroundPID: 200, ForegroundWindow: "other"}, true, "foreground", "needs_user", ""},
		{"explicit background unsupported", idle(), false, "background", "unsupported", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, d, m, _ := setup(t)
			a := action(t, e, "a1")
			a.Mode = tc.mode
			d.background = tc.background
			m.set(tc.a)
			d.execute = func(_ context.Context, _ model.Action, _ model.View, mode string) model.Outcome {
				if mode != tc.runMode {
					t.Errorf("mode=%s", mode)
				}
				return model.Outcome{State: "succeeded"}
			}
			r := execute(t, e, a)
			if r.State != tc.want {
				t.Fatalf("state=%s", r.State)
			}
		})
	}
}

func TestReadOnlyCheckRechecksHumanAndTarget(t *testing.T) {
	e, d, m, _ := setup(t)
	a := action(t, e, "a1")
	d.check = func(context.Context, model.View) error {
		m.set(model.Activity{Available: true, Generation: 2, ForegroundPID: 200, ForegroundWindow: "other"})
		return nil
	}
	r := execute(t, e, a)
	if r.State != "needs_user" || d.calls.Load() != 0 {
		t.Fatalf("human changed during check: %+v", r)
	}
	e2, d2, _, _ := setup(t)
	a = action(t, e2, "a2")
	d2.check = func(context.Context, model.View) error { return errors.New("PRIVATE CHECK ERROR") }
	r = execute(t, e2, a)
	if r.State != "needs_user" || d2.calls.Load() != 0 || strings.Contains(r.Message, "PRIVATE") {
		t.Fatalf("target check not enforced: %+v", r)
	}
}

func TestHumanTakeoverCancelsAndReleases(t *testing.T) {
	for _, background := range []bool{false, true} {
		t.Run(map[bool]string{false: "foreground", true: "background"}[background], func(t *testing.T) {
			e, d, m, _ := setup(t)
			d.background = background
			a := action(t, e, "a1")
			started := make(chan struct{})
			d.execute = func(ctx context.Context, _ model.Action, _ model.View, _ string) model.Outcome {
				close(started)
				<-ctx.Done()
				return model.Outcome{State: "succeeded"}
			}
			done := make(chan model.Receipt, 1)
			go func() { r, _ := e.Execute(context.Background(), a); done <- r }()
			<-started
			next := model.Activity{Available: true, Generation: 2, ForegroundPID: 200, ForegroundWindow: "other"}
			if background {
				next.ForegroundPID = 100
				next.ForegroundWindow = "win-100"
			}
			m.set(next)
			select {
			case r := <-done:
				if r.State != "unknown" {
					t.Fatalf("takeover=%s", r.State)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("human takeover not cancelled")
			}
			if d.releases.Load() == 0 {
				t.Fatal("held inputs not released")
			}
		})
	}
}

func TestBackgroundAllowsActivityInOtherApp(t *testing.T) {
	e, d, m, _ := setup(t)
	d.background = true
	a := action(t, e, "a1")
	started := make(chan struct{})
	finish := make(chan struct{})
	d.execute = func(ctx context.Context, _ model.Action, _ model.View, _ string) model.Outcome {
		close(started)
		select {
		case <-ctx.Done():
			return model.Outcome{State: "unknown"}
		case <-finish:
			return model.Outcome{State: "succeeded"}
		}
	}
	done := make(chan model.Receipt, 1)
	go func() { r, _ := e.Execute(context.Background(), a); done <- r }()
	<-started
	m.set(model.Activity{Available: true, Generation: 2, ForegroundPID: 200, ForegroundWindow: "other"})
	time.Sleep(50 * time.Millisecond)
	close(finish)
	r := <-done
	if r.State != "succeeded" {
		t.Fatalf("other app interrupted background: %+v", r)
	}
}

func TestCancelPersistsAndDoesNotAliasTaskIDs(t *testing.T) {
	e, d, m, cfg := setup(t)
	a := action(t, e, "same-id")
	execute(t, e, a)
	a = action(t, e, "same-id")
	a.TaskID = "task-2"
	execute(t, e, a)
	if d.calls.Load() != 2 {
		t.Fatal("action ids incorrectly global")
	}
	if err := e.Cancel("task-1"); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	e2, err := New(cfg, d, m)
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()
	a = action(t, e2, "new-id")
	r := execute(t, e2, a)
	if r.State != "cancelled" || d.calls.Load() != 2 {
		t.Fatalf("cancel not persisted: %+v", r)
	}
}

func TestCancellationLateSuccessIsUnknownAndBlocksSecondWriter(t *testing.T) {
	e, d, _, _ := setup(t)
	a := action(t, e, "a1")
	started := make(chan struct{})
	finish := make(chan struct{})
	d.execute = func(context.Context, model.Action, model.View, string) model.Outcome {
		close(started)
		<-finish
		return model.Outcome{State: "succeeded"}
	}
	done := make(chan model.Receipt, 1)
	go func() { r, _ := e.Execute(context.Background(), a); done <- r }()
	<-started
	if err := e.Cancel("task-1"); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		if r.State != "unknown" {
			t.Fatalf("cancel=%s", r.State)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("cancel waited for unresponsive driver")
	}
	if d.releases.Load() == 0 {
		t.Fatal("release absent")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if _, err := e.Observe(ctx, target); err == nil {
		t.Fatal("observation ran while old driver alive")
	}
	close(finish)
}

func TestConcurrentSameActionExecutesOnce(t *testing.T) {
	e, d, _, _ := setup(t)
	a := action(t, e, "a1")
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := e.Execute(context.Background(), a)
			if err != nil || r.State != "succeeded" {
				t.Errorf("concurrent receipt=%+v err=%v", r, err)
			}
		}()
	}
	wg.Wait()
	if d.calls.Load() != 1 {
		t.Fatalf("calls=%d", d.calls.Load())
	}
}

func TestRestartCachesAndCrashDispatchBecomesUnknown(t *testing.T) {
	e, d, m, cfg := setup(t)
	a := action(t, e, "a1")
	execute(t, e, a)
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	e2, err := New(cfg, d, m)
	if err != nil {
		t.Fatal(err)
	}
	r := execute(t, e2, a)
	if r.State != "succeeded" || d.calls.Load() != 1 {
		t.Fatal("restart duplicate repeated")
	}
	e2.Close()
	path := filepath.Join(cfg.StateDir, "ledger.jsonl")
	b, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	if len(lines) < 2 {
		t.Fatal("missing write-ahead event")
	}
	if err = os.WriteFile(path, []byte(strings.Join(lines[:len(lines)-1], "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	e3, err := New(cfg, d, m)
	if err != nil {
		t.Fatal(err)
	}
	defer e3.Close()
	r = execute(t, e3, a)
	if r.State != "unknown" || d.calls.Load() != 1 {
		t.Fatalf("crash retry dispatched: %+v", r)
	}
}

func TestStateLockAndMalformedLedgerFailClosed(t *testing.T) {
	e, d, m, cfg := setup(t)
	if duplicate, err := New(cfg, d, m); err == nil {
		duplicate.Close()
		t.Fatal("second engine got state lock")
	}
	e.Close()
	f, err := os.OpenFile(filepath.Join(cfg.StateDir, "ledger.jsonl"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("{broken\n")
	f.Close()
	if broken, err := New(cfg, d, m); err == nil {
		broken.Close()
		t.Fatal("malformed WAL silently accepted")
	}
}

func TestPausePersistsAndStorageFailurePreventsDispatch(t *testing.T) {
	e, d, m, cfg := setup(t)
	if err := e.Pause(true); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Apps(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Observe(context.Background(), target); code(err) != "paused" {
		t.Fatalf("paused observe=%v", err)
	}
	e.Close()
	e2, err := New(cfg, d, m)
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()
	if !e2.Device().Paused {
		t.Fatal("pause lost on restart")
	}
	if err = e2.Pause(false); err != nil {
		t.Fatal(err)
	}
	a := action(t, e2, "a1")
	e2.syncFile = func(*os.File) error { return errors.New("private sync failure") }
	if _, err = e2.Execute(context.Background(), a); code(err) != "storage_unavailable" {
		t.Fatalf("sync err=%v", err)
	}
	if d.calls.Load() != 0 {
		t.Fatal("dispatch before durable journal")
	}
	if _, err = e2.Observe(context.Background(), target); code(err) != "storage_unavailable" {
		t.Fatalf("storage failure not sticky: %v", err)
	}
}

func TestConcurrentDuplicateWaitsForFinalReceipt(t *testing.T) {
	e, d, _, _ := setup(t)
	a := action(t, e, "a1")
	started := make(chan struct{})
	finish := make(chan struct{})
	d.execute = func(context.Context, model.Action, model.View, string) model.Outcome {
		close(started)
		<-finish
		return model.Outcome{State: "succeeded"}
	}
	first := make(chan model.Receipt, 1)
	go func() { r, _ := e.Execute(context.Background(), a); first <- r }()
	<-started
	second := make(chan model.Receipt, 1)
	go func() { r, _ := e.Execute(context.Background(), a); second <- r }()
	select {
	case r := <-second:
		t.Fatalf("duplicate returned intermediate state: %s", r.State)
	case <-time.After(30 * time.Millisecond):
	}
	close(finish)
	if (<-first).State != "succeeded" || (<-second).State != "succeeded" {
		t.Fatal("duplicate did not receive final state")
	}
}

func TestObservationCallerCannotChangeStoredTokens(t *testing.T) {
	e, d, _, _ := setup(t)
	o, err := e.Observe(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	o.View.Elements[0] = "forged-token"
	a := model.Action{DeviceID: e.cfg.DeviceID, TaskID: "task-1", ActionID: "a1", ObservationID: o.ID, Target: target, Kind: "click", Mode: "auto", Args: json.RawMessage(`{"element_token":"forged-token"}`)}
	if _, err = e.Execute(context.Background(), a); code(err) != "invalid_element" {
		t.Fatalf("caller forged token: %v", err)
	}
	if d.calls.Load() != 0 {
		t.Fatal("forged token reached driver")
	}
}

func TestCloseRetainsProcessLockUntilUncooperativeDriverReturns(t *testing.T) {
	e, d, m, cfg := setup(t)
	a := action(t, e, "a1")
	started := make(chan struct{})
	finish := make(chan struct{})
	d.execute = func(context.Context, model.Action, model.View, string) model.Outcome {
		close(started)
		<-finish
		return model.Outcome{State: "succeeded"}
	}
	go e.Execute(context.Background(), a)
	<-started
	closed := make(chan struct{})
	go func() { _ = e.Close(); close(closed) }()
	time.Sleep(30 * time.Millisecond)
	if other, err := New(cfg, d, m); err == nil {
		other.Close()
		close(finish)
		t.Fatal("Close released process lock while native execution remained alive")
	}
	close(finish)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not finish after driver returned")
	}
}

func TestTimeoutBecomesUnknown(t *testing.T) {
	e, d, _, _ := setup(t)
	e.cfg.ActionTimeout = 30 * time.Millisecond
	a := action(t, e, "a1")
	d.execute = func(ctx context.Context, _ model.Action, _ model.View, _ string) model.Outcome {
		<-ctx.Done()
		return model.Outcome{State: "succeeded"}
	}
	r := execute(t, e, a)
	if r.State != "unknown" {
		t.Fatalf("late response promoted: %+v", r)
	}
}

func TestActivityLossDuringExecutionIsUnknown(t *testing.T) {
	e, d, m, _ := setup(t)
	a := action(t, e, "a1")
	started := make(chan struct{})
	d.execute = func(ctx context.Context, _ model.Action, _ model.View, _ string) model.Outcome {
		close(started)
		<-ctx.Done()
		return model.Outcome{State: "succeeded"}
	}
	done := make(chan model.Receipt, 1)
	go func() { r, _ := e.Execute(context.Background(), a); done <- r }()
	<-started
	m.set(model.Activity{})
	select {
	case r := <-done:
		if r.State != "unknown" {
			t.Fatalf("lost monitor=%s", r.State)
		}
	case <-time.After(time.Second):
		t.Fatal("monitor loss did not cancel")
	}
}

func TestReopeningUnderDifferentDeviceFails(t *testing.T) {
	e, d, m, cfg := setup(t)
	a := action(t, e, "a1")
	execute(t, e, a)
	e.Close()
	cfg.DeviceID = "other-device"
	if other, err := New(cfg, d, m); err == nil {
		other.Close()
		t.Fatal("ledger accepted under different device")
	}
}

func TestUnknownInvalidatesOtherOlderObservations(t *testing.T) {
	e, d, _, _ := setup(t)
	a := action(t, e, "a1")
	other := model.Target{PID: 101, WindowID: "other-101"}
	o, err := e.Observe(context.Background(), other)
	if err != nil {
		t.Fatal(err)
	}
	d.execute = func(context.Context, model.Action, model.View, string) model.Outcome {
		return model.Outcome{State: "unknown"}
	}
	if r := execute(t, e, a); r.State != "unknown" {
		t.Fatal(r.State)
	}
	a.ActionID = "a2"
	a.ObservationID = o.ID
	a.Target = other
	if _, err = e.Execute(context.Background(), a); code(err) != "stale_observation" {
		t.Fatalf("older observation survived unknown: %v", err)
	}
}

func TestHumanActivityDuringJournalSyncPreventsNativeDispatch(t *testing.T) {
	e, d, m, _ := setup(t)
	a := action(t, e, "a1")
	e.syncFile = func(f *os.File) error {
		err := f.Sync()
		m.set(model.Activity{Available: true, Generation: 2, ForegroundPID: 200, ForegroundWindow: "other"})
		return err
	}
	r := execute(t, e, a)
	if r.State != "unknown" || d.calls.Load() != 0 {
		t.Fatalf("dispatched after human activity during sync: receipt=%+v calls=%d", r, d.calls.Load())
	}
}

func TestFailedFinalSyncNeverReportsSuccess(t *testing.T) {
	e, d, _, _ := setup(t)
	a := action(t, e, "a1")
	var n atomic.Int32
	e.syncFile = func(f *os.File) error {
		if n.Add(1) == 2 {
			return errors.New("disk failed")
		}
		return f.Sync()
	}
	r, err := e.Execute(context.Background(), a)
	if code(err) != "storage_unavailable" || r.State == "succeeded" || d.calls.Load() != 1 {
		t.Fatalf("non-durable success: %+v %v", r, err)
	}
}

func TestDefaultIdleThresholdAndFreshDeviceNonce(t *testing.T) {
	d := &testDriver{}
	m := &testMonitor{activity: idle()}
	e, err := New(Config{DeviceID: "device-1", StateDir: t.TempDir()}, d, m)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	a := action(t, e, "a1")
	if r := execute(t, e, a); r.State != "needs_user" {
		t.Fatalf("default threshold did not require 60 seconds: %+v", r)
	}
	one, two := e.Device(), e.Device()
	if one.Nonce == "" || one.Nonce == two.Nonce {
		t.Fatal("device nonce not fresh")
	}
}

func TestCancellationDuringReleaseCannotBecomeSuccess(t *testing.T) {
	e, d, _, _ := setup(t)
	a := action(t, e, "a1")
	releasing := make(chan struct{})
	finish := make(chan struct{})
	d.releaseFn = func(context.Context) error { close(releasing); <-finish; return nil }
	done := make(chan model.Receipt, 1)
	go func() { r, _ := e.Execute(context.Background(), a); done <- r }()
	<-releasing
	if err := e.Cancel("task-1"); err != nil {
		t.Fatal(err)
	}
	close(finish)
	if r := <-done; r.State != "unknown" {
		t.Fatalf("cancel during release=%s", r.State)
	}
}

func TestClosedEngineReadCallsReturnImmediately(t *testing.T) {
	e, _, _, _ := setup(t)
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := e.Apps(ctx); code(err) != "closed" {
		t.Fatalf("closed Apps=%v", err)
	}
	if _, err := e.Observe(ctx, target); code(err) != "closed" {
		t.Fatalf("closed Observe=%v", err)
	}
}

func TestCrashDispatchSubprocess(t *testing.T) {
	dir := os.Getenv("DOTCONTROL_ENGINE_CRASH_DIR")
	if dir == "" {
		t.Skip("subprocess-only crash point")
	}
	d := &testDriver{}
	m := &testMonitor{activity: idle()}
	e, err := New(Config{DeviceID: "device-1", StateDir: dir, IdleThreshold: time.Second}, d, m)
	if err != nil {
		t.Fatal(err)
	}
	a := action(t, e, "crash-action")
	b, _ := json.Marshal(a)
	if err = os.WriteFile(os.Getenv("DOTCONTROL_ENGINE_CRASH_REQUEST"), b, 0600); err != nil {
		t.Fatal(err)
	}
	d.execute = func(context.Context, model.Action, model.View, string) model.Outcome {
		os.Exit(73)
		return model.Outcome{}
	}
	e.Execute(context.Background(), a)
	t.Fatal("crash point did not terminate process")
}

func TestActualProcessCrashRecoversWithoutRepeating(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	requestPath := filepath.Join(t.TempDir(), "request.json")
	cmd := exec.Command(exe, "-test.run=^TestCrashDispatchSubprocess$")
	cmd.Env = append(os.Environ(), "DOTCONTROL_ENGINE_CRASH_DIR="+dir, "DOTCONTROL_ENGINE_CRASH_REQUEST="+requestPath)
	b, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 73 {
		t.Fatalf("crash helper exit=%v output=%s", err, b)
	}
	b, err = os.ReadFile(requestPath)
	if err != nil {
		t.Fatal(err)
	}
	var a model.Action
	if err = json.Unmarshal(b, &a); err != nil {
		t.Fatal(err)
	}
	d := &testDriver{}
	m := &testMonitor{activity: idle()}
	e, err := New(Config{DeviceID: "device-1", StateDir: dir, IdleThreshold: time.Second}, d, m)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	r := execute(t, e, a)
	if r.State != "unknown" || d.calls.Load() != 0 {
		t.Fatalf("crashed action repeated: %+v", r)
	}
	a = action(t, e, "fresh-action")
	if r = execute(t, e, a); r.State != "succeeded" || d.calls.Load() != 1 {
		t.Fatal("fresh observation did not permit recovery")
	}
}

func TestPartialLedgerLineFailsWithoutTruncation(t *testing.T) {
	e, d, m, cfg := setup(t)
	a := action(t, e, "a1")
	execute(t, e, a)
	e.Close()
	path := filepath.Join(cfg.StateDir, "ledger.jsonl")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"payload":`)
	f.Close()
	before, _ := os.ReadFile(path)
	if bad, err := New(cfg, d, m); err == nil {
		bad.Close()
		t.Fatal("partial line accepted")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("corrupt WAL was silently truncated")
	}
}

func TestControlChangesDuringCheckCannotDispatch(t *testing.T) {
	for _, change := range []string{"pause_resume", "cancel_task", "cancel_request"} {
		t.Run(change, func(t *testing.T) {
			e, d, _, _ := setup(t)
			a := action(t, e, "a1")
			checking := make(chan struct{})
			finishCheck := make(chan struct{})
			d.check = func(context.Context, model.View) error { close(checking); <-finishCheck; return nil }
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan runResult, 1)
			go func() { r, err := e.Execute(ctx, a); done <- runResult{receipt: r, err: err} }()
			<-checking
			switch change {
			case "pause_resume":
				if err := e.Pause(true); err != nil {
					t.Fatal(err)
				}
				if err := e.Pause(false); err != nil {
					t.Fatal(err)
				}
			case "cancel_task":
				if err := e.Cancel(a.TaskID); err != nil {
					t.Fatal(err)
				}
			case "cancel_request":
				cancel()
			}
			close(finishCheck)
			result := <-done
			if change == "pause_resume" {
				if code(result.err) != "stale_observation" {
					t.Fatalf("invalidated observation dispatched: receipt=%+v err=%v", result.receipt, result.err)
				}
			} else if result.err != nil || result.receipt.State != "cancelled" {
				t.Fatalf("cancel not observed: receipt=%+v err=%v", result.receipt, result.err)
			}
			if d.calls.Load() != 0 {
				t.Fatalf("native dispatch count=%d", d.calls.Load())
			}
			if change == "pause_resume" {
				d.check = nil
				a = action(t, e, "fresh-action")
				if r := execute(t, e, a); r.State != "succeeded" {
					t.Fatalf("fresh observation after resume rejected: %+v", r)
				}
			}
		})
	}
}

func TestObservationExpiryDuringJournalSyncPreventsNativeDispatch(t *testing.T) {
	e, d, _, _ := setup(t)
	e.cfg.ObservationTTL = 200 * time.Millisecond
	a := action(t, e, "a1")
	var writes atomic.Int32
	e.syncFile = func(f *os.File) error {
		if writes.Add(1) == 1 {
			time.Sleep(250 * time.Millisecond)
		}
		return f.Sync()
	}
	r := execute(t, e, a)
	if r.State != "unknown" || d.calls.Load() != 0 {
		t.Fatalf("expired observation dispatched after journal sync: receipt=%+v calls=%d", r, d.calls.Load())
	}
	retry := execute(t, e, a)
	if retry.State != "unknown" || d.calls.Load() != 0 {
		t.Fatalf("expired intent retry dispatched: receipt=%+v calls=%d", retry, d.calls.Load())
	}
}
