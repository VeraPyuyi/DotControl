package dotqueue

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Two independent child OS processes compete for the single remaining slot of a
// store that already retains maxRetainedJobs-1 jobs, each submitting its own
// distinct request ID.
//
// Synchronization is file/pipe based, not a timed sleep: every child first
// publishes a ready file, then blocks reading one byte from its inherited stdin
// pipe. The parent waits for both ready files and only then writes the release
// byte, so both children are provably parked at the same program point when the
// contention starts. Which one wins the queue lock is then decided by the lock
// itself, never by scheduling luck.
//
// Child processes use only temporary fixture state and a fixed, non-secret test key.
func TestCapacityLastSlotCrossProcess(t *testing.T) {
	store, dir := openTestStore(t)
	fillStoreJobs(t, store, 0, maxRetainedJobs-1, "client")
	if c, err := store.Capacity(); err != nil || c.Retained != maxRetainedJobs-1 || c.Remaining != 1 {
		t.Fatalf("pre-race capacity: %v %#v", err, c)
	}

	handshake := t.TempDir()
	type racer struct {
		index     int
		requestID string
		outcome   string
		jobID     string
	}
	racers := make([]racer, 2)
	for i := range racers {
		racers[i] = racer{index: i, requestID: fmt.Sprintf("req.last.%d", i)}
	}

	cmds := make([]*exec.Cmd, 2)
	logs := make([]*bytes.Buffer, 2)
	release := make([]*os.File, 2)
	ready := make([]string, 2)
	result := make([]string, 2)

	// Phase 1: spawn both children and wait until both are parked on the pipe.
	for i := range racers {
		ready[i] = filepath.Join(handshake, fmt.Sprintf("ready-%d", i))
		result[i] = filepath.Join(handshake, fmt.Sprintf("result-%d", i))
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		release[i] = w
		logs[i] = &bytes.Buffer{}
		cmd := exec.Command(os.Args[0], "-test.run=^TestCapacityLastSlotProcessHelper$", "-test.v")
		cmd.Env = append(os.Environ(),
			"DOTQUEUE_LAST_SLOT_TEST_DIR="+dir,
			"DOTQUEUE_LAST_SLOT_TEST_REQUEST="+racers[i].requestID,
			"DOTQUEUE_LAST_SLOT_TEST_READY="+ready[i],
			"DOTQUEUE_LAST_SLOT_TEST_RESULT="+result[i],
			"DOTQUEUE_LAST_SLOT_TEST_RELEASE=stdin",
		)
		cmd.Stdin = r
		cmd.Stdout = logs[i]
		cmd.Stderr = logs[i]
		if err := cmd.Start(); err != nil {
			r.Close()
			w.Close()
			t.Fatal(err)
		}
		r.Close()
		cmds[i] = cmd
	}
	for i := range racers {
		if err := waitForFile(ready[i], 30*time.Second); err != nil {
			t.Fatalf("racer %d never became ready: %v %s", i, err, logs[i])
		}
	}

	// Phase 2: release both at once and let the queue lock decide the winner.
	for i := range racers {
		if _, err := release[i].Write([]byte{1}); err != nil {
			t.Fatal(err)
		}
		if err := release[i].Close(); err != nil {
			t.Fatal(err)
		}
	}
	for i := range racers {
		if err := cmds[i].Wait(); err != nil {
			t.Fatalf("racer %d failed: %v %s", i, err, logs[i])
		}
	}

	for i := range racers {
		data, err := os.ReadFile(result[i])
		if err != nil {
			t.Fatalf("racer %d result missing: %v %s", i, err, logs[i])
		}
		fields := strings.Fields(string(data))
		if len(fields) == 0 {
			t.Fatalf("racer %d result empty: %s", i, logs[i])
		}
		racers[i].outcome = fields[0]
		if len(fields) > 1 {
			racers[i].jobID = fields[1]
		}
	}

	// Phase 3: exactly one success and exactly one queue_full.
	success, rejected := 0, 0
	winner, loser := -1, -1
	for i := range racers {
		switch racers[i].outcome {
		case "ok":
			success++
			winner = i
			if racers[i].jobID == "" {
				t.Fatalf("racer %d reported ok without a job id", i)
			}
		case "queue_full":
			rejected++
			loser = i
			if racers[i].jobID != "" {
				t.Fatalf("racer %d reported queue_full with a job id %q", i, racers[i].jobID)
			}
		default:
			t.Fatalf("racer %d unexpected outcome %q %s", i, racers[i].outcome, logs[i])
		}
	}
	if success != 1 || rejected != 1 {
		t.Fatalf("last slot outcomes: success=%d queue_full=%d", success, rejected)
	}

	// Phase 4: the winning write is durable and caps the global retained count.
	cap, err := store.Capacity()
	if err != nil || cap.Retained != maxRetainedJobs || cap.Remaining != 0 || cap.Limit != maxRetainedJobs {
		t.Fatalf("capacity after race: %v %#v", err, cap)
	}
	reopened, err := Open(dir, testKey)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := reopened.Capacity()
	if err != nil || c2 != cap {
		t.Fatalf("capacity changed on reopen: before=%#v after=%#v err=%v", cap, c2, err)
	}

	// Phase 5: winner present and stable after reopen, loser absent.
	job, err := reopened.ByRequest("client.child", racers[winner].requestID)
	if err != nil {
		t.Fatalf("winner lookup: %v", err)
	}
	if job.ID != racers[winner].jobID || job.RequestID != racers[winner].requestID ||
		job.ClientID != "client.child" || job.QueueID != "client.child" || job.State != StateQueued {
		t.Fatalf("winner job after reopen: %#v", job)
	}
	if loserJob := racers[loser].jobID; loserJob != "" {
		t.Fatalf("loser returned a job id %q", loserJob)
	}
	if _, err := reopened.ByRequest("client.child", racers[loser].requestID); errorCode(err) != "notfound" {
		t.Fatalf("loser present after reopen: %v", err)
	}

	// Phase 6: at full capacity the winner stays idempotent and the loser stays refused.
	if res := runLastSlotChild(t, dir, racers[winner].requestID); res != "ok "+racers[winner].jobID {
		t.Fatalf("winner retry at capacity: %q", res)
	}
	if res := runLastSlotChild(t, dir, racers[loser].requestID); res != "queue_full" {
		t.Fatalf("loser retry at capacity: %q", res)
	}
	again, err := reopened.ByRequest("client.child", racers[winner].requestID)
	if err != nil || again.ID != racers[winner].jobID || again.RequestDigest != job.RequestDigest {
		t.Fatalf("winner changed after idempotent retry: %v %#v", err, again)
	}
	if _, err := reopened.ByRequest("client.child", racers[loser].requestID); errorCode(err) != "notfound" {
		t.Fatalf("loser present after refused retry: %v", err)
	}
	if c3, err := reopened.Capacity(); err != nil || c3 != cap {
		t.Fatalf("capacity changed by refused retries: %v %#v", err, c3)
	}

	// Phase 7: the retained state stays encrypted on disk.
	data, err := os.ReadFile(filepath.Join(dir, stateFileName))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"hello dot", racers[winner].requestID, racers[loser].requestID,
		"req.cap.", "client.child", "sess.1", racers[winner].jobID} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("plaintext %q found in state file", secret)
		}
	}
}

// runLastSlotChild submits requestID through a fresh child process with no
// release synchronization, for post-race assertions at capacity.
func runLastSlotChild(t *testing.T, dir, requestID string) string {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCapacityLastSlotProcessHelper$", "-test.v")
	cmd.Env = append(os.Environ(),
		"DOTQUEUE_LAST_SLOT_TEST_DIR="+dir,
		"DOTQUEUE_LAST_SLOT_TEST_REQUEST="+requestID,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child failed: %v %s", err, out)
	}
	body := strings.TrimSpace(string(out))
	for _, line := range strings.Split(body, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "RESULT="); ok {
			return strings.TrimSpace(v)
		}
	}
	t.Fatalf("child result missing: %s", body)
	return ""
}

func waitForFile(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for %s", path)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestCapacityLastSlotProcessHelper is the child-process entry point. Without
// fixture environment it is a no-op, so it never interferes with normal runs.
func TestCapacityLastSlotProcessHelper(t *testing.T) {
	dir := os.Getenv("DOTQUEUE_LAST_SLOT_TEST_DIR")
	if dir == "" {
		return
	}
	requestID := os.Getenv("DOTQUEUE_LAST_SLOT_TEST_REQUEST")
	if requestID == "" {
		t.Fatal("missing request id")
	}
	if os.Getenv("DOTQUEUE_LAST_SLOT_TEST_RELEASE") == "stdin" {
		if err := os.WriteFile(os.Getenv("DOTQUEUE_LAST_SLOT_TEST_READY"), []byte("ready\n"), 0600); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 1)
		if _, err := os.Stdin.Read(buf); err != nil {
			t.Fatal(err)
		}
	}
	store, err := Open(dir, testKey)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.Submit("client.child", sampleSubmission(requestID))
	line := "queue_full"
	if err == nil {
		line = "ok " + job.ID
	} else if errorCode(err) != "queue_full" {
		line = "error " + errorCode(err)
	}
	if p := os.Getenv("DOTQUEUE_LAST_SLOT_TEST_RESULT"); p != "" {
		if err := os.WriteFile(p, []byte(line+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		return
	}
	fmt.Println("RESULT=" + line)
}
