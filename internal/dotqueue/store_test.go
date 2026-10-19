package dotqueue

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

var testKey = []byte{
	0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
	0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10,
	0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18,
	0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f, 0x20,
}

func openTestStore(t *testing.T) (*Store, string) {
	dir := t.TempDir()
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	store, err := Open(abs, testKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Initialize(true); err != nil {
		t.Fatal(err)
	}
	return store, abs
}

func sampleSubmission(requestID string) Submission {
	return Submission{
		RequestID: requestID,
		Mode:      ModeAnswer,
		SessionID: "sess.1",
		Messages:  []Message{{Role: "user", Content: "hello dot"}},
	}
}

func TestEncryptedRoundTripAndReopen(t *testing.T) {
	store, dir := openTestStore(t)
	job, err := store.Submit("client.a", sampleSubmission("req.1"))
	if err != nil {
		t.Fatal(err)
	}
	if job.State != StateQueued || job.QueueID != "client.a" {
		t.Fatalf("unexpected job %#v", job)
	}
	reopened, err := Open(dir, testKey)
	if err != nil {
		t.Fatal(err)
	}
	again, err := reopened.ByRequest("client.a", "req.1")
	if err != nil || again.ID != job.ID {
		t.Fatalf("persisted lookup failed: %v %#v", err, again)
	}
	data, err := os.ReadFile(filepath.Join(dir, stateFileName))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("hello dot")) {
		t.Fatal("plaintext prompt found on disk")
	}
}

func TestSubmitIdempotencyAndConflict(t *testing.T) {
	store, _ := openTestStore(t)
	sub := sampleSubmission("req.idem")
	first, err := store.Submit("client.a", sub)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Submit("client.a", sub)
	if err != nil || second.ID != first.ID {
		t.Fatalf("idempotent submit failed: %v %#v", err, second)
	}
	changed := sub
	changed.Messages[0].Content = "changed"
	_, err = store.Submit("client.a", changed)
	if errorCode(err) != "conflict" {
		t.Fatalf("expected conflict, got %v", err)
	}
}

func TestClaimLifecycleAndResultIdempotency(t *testing.T) {
	store, _ := openTestStore(t)
	job, err := store.Submit("client.a", sampleSubmission("req.claim"))
	if err != nil {
		t.Fatal(err)
	}
	notices, err := store.Pending("client.a")
	if err != nil || len(notices) != 1 || notices[0].JobID != job.ID {
		t.Fatalf("pending notice mismatch: %v %#v", err, notices)
	}
	claim1, err := store.Claim("client.a", job.ID, "worker.1")
	if err != nil {
		t.Fatal(err)
	}
	claim2, err := store.Claim("client.a", job.ID, "worker.1")
	if err != nil || claim2.Token != claim1.Token {
		t.Fatalf("duplicate worker claim failed: %v", err)
	}
	_, err = store.Claim("client.a", job.ID, "worker.2")
	if errorCode(err) != "claim_conflict" {
		t.Fatalf("expected claim_conflict, got %v", err)
	}
	_, err = store.Progress(job.ID, claim1.Token, StateRunning, "working")
	if err != nil {
		t.Fatal(err)
	}
	res := Result{Text: "done", Artifacts: []Artifact{{URL: "https://example.com/a", Label: "a"}}}
	done1, err := store.Complete(job.ID, claim1.Token, res)
	if err != nil || done1.State != StateCompleted {
		t.Fatalf("complete failed: %v %#v", err, done1)
	}
	done2, err := store.Complete(job.ID, claim1.Token, res)
	if err != nil || done2.State != StateCompleted {
		t.Fatalf("idempotent complete failed: %v", err)
	}
	res.Text = "different"
	_, err = store.Complete(job.ID, claim1.Token, res)
	if errorCode(err) != "conflict" {
		t.Fatalf("expected result conflict, got %v", err)
	}
}

func TestCancelIntentAndRecover(t *testing.T) {
	store, _ := openTestStore(t)
	queued, err := store.Submit("client.a", sampleSubmission("req.cancel.queued"))
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := store.Cancel("client.a", queued.ID)
	if err != nil || cancelled.State != StateCancelled {
		t.Fatalf("queued cancel failed: %v %#v", err, cancelled)
	}
	active, err := store.Submit("client.a", sampleSubmission("req.cancel.active"))
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.Claim("client.a", active.ID, "worker.1")
	if err != nil {
		t.Fatal(err)
	}
	requested, err := store.Cancel("client.a", active.ID)
	if err != nil || requested.State != StateCancelRequested || !requested.CancelRequested {
		t.Fatalf("active cancel failed: %v %#v", err, requested)
	}
	_, err = store.Progress(active.ID, claim.Token, StateRunning, "still running")
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.Get("client.a", active.ID)
	if err != nil || got.State != StateCancelRequested {
		t.Fatalf("progress preserved cancel intent: %v %#v", err, got)
	}
	if err := store.Recover(); err != nil {
		t.Fatal(err)
	}
	recovered, err := store.Get("client.a", active.ID)
	if err != nil || recovered.State != StateOutcomeUnknown {
		t.Fatalf("recover failed: %v %#v", err, recovered)
	}
	if err := store.Recover(); err != nil {
		t.Fatal(err)
	}
	again, err := store.Get("client.a", active.ID)
	if err != nil || again.State != StateOutcomeUnknown {
		t.Fatalf("recover should be once: %#v", again)
	}
}

func TestDisabledRejectsSensitiveOps(t *testing.T) {
	store, _ := openTestStore(t)
	if err := store.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	_, err := store.Submit("client.a", sampleSubmission("req.disabled"))
	if errorCode(err) != "disabled" {
		t.Fatalf("expected disabled, got %v", err)
	}
	if err := store.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	job, err := store.Submit("client.a", sampleSubmission("req.enabled"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	_, err = store.Claim("client.a", job.ID, "worker.1")
	if errorCode(err) != "disabled" {
		t.Fatalf("claim should be disabled: %v", err)
	}
	_, err = store.Get("client.a", job.ID)
	if err == nil || err.Error() != "dot_queue_disabled" {
		t.Fatalf("read should be disabled: %v", err)
	}
}

func TestMissingStateFailsClosed(t *testing.T) {
	store, dir := openTestStore(t)
	if err := os.WriteFile(filepath.Join(dir, markerName), []byte("1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, stateFileName)); err != nil {
		t.Fatal(err)
	}
	_, err := store.Get("client.a", "job.missing")
	if errorCode(err) != "corrupt" {
		t.Fatalf("expected corrupt for missing state, got %v", err)
	}
}

func TestCorruptEncryptedState(t *testing.T) {
	store, dir := openTestStore(t)
	_, err := store.Submit("client.a", sampleSubmission("req.corrupt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, stateFileName), []byte("DQ1bad"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = store.ByRequest("client.a", "req.corrupt")
	if errorCode(err) != "corrupt" {
		t.Fatalf("expected corrupt, got %v", err)
	}
}

func TestBoundsValidation(t *testing.T) {
	store, _ := openTestStore(t)
	big := sampleSubmission("req.big")
	big.Messages = make([]Message, maxMessages+1)
	_, err := store.Submit("client.a", big)
	if errorCode(err) != "unknown" {
		t.Fatalf("expected bound rejection, got %v", err)
	}
	taskOnly := Submission{RequestID: "req.task", Mode: ModeTask, Task: "do thing"}
	_, err = store.Submit("client.a", taskOnly)
	if err != nil {
		t.Fatalf("task mode should be valid: %v", err)
	}
	emptyTask := Submission{RequestID: "req.empty", Mode: ModeTask, Task: "   "}
	_, err = store.Submit("client.a", emptyTask)
	if errorCode(err) != "unknown" {
		t.Fatalf("empty task rejected: %v", err)
	}
}

func TestConcurrentSubmitAndClaim(t *testing.T) {
	store, dir := openTestStore(t)
	other, err := Open(dir, testKey)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	const n = 16
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			reqID := fmt.Sprintf("req.parallel.%d", i)
			job, err := store.Submit("client.a", sampleSubmission(reqID))
			if err != nil {
				t.Errorf("submit %d: %v", i, err)
				return
			}
			ids[i] = job.ID
		}(i)
	}
	wg.Wait()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			if ids[i] == "" {
				continue
			}
			_, err := other.Claim("client.a", ids[i], "worker.1")
			if err != nil {
				t.Errorf("claim %d: %v", i, err)
			}
		}
	}()
	wg.Wait()
}

func TestMaxRetainedJobs(t *testing.T) {
	store, _ := openTestStore(t)
	for i := 0; i < 100; i++ {
		req := sampleSubmission(fmt.Sprintf("req.cap.%d", i))
		if _, err := store.Submit("client.a", req); err != nil {
			t.Fatalf("fill %d: %v", i, err)
		}
	}
	_, err := store.Submit("client.a", sampleSubmission("req.overflow"))
	if errorCode(err) != "conflict" {
		t.Fatalf("expected capacity conflict, got %v", err)
	}
}
