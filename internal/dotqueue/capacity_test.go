package dotqueue

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func fillStoreJobs(t *testing.T, store *Store, start int, count int, clientPrefix string) map[string]Job {
	t.Helper()
	jobs := make(map[string]Job, count)
	for i := 0; i < count; i++ {
		idx := start + i
		clientID := fmt.Sprintf("%s.%d", clientPrefix, idx%10)
		reqID := fmt.Sprintf("req.cap.%d", idx)
		job, err := store.Submit(clientID, sampleSubmission(reqID))
		if err != nil {
			t.Fatalf("fill %d: %v", idx, err)
		}
		jobs[reqID] = job
	}
	return jobs
}

func TestCapacityStatusWhileFilling(t *testing.T) {
	store, _ := openTestStore(t)
	cap, err := store.Capacity()
	if err != nil {
		t.Fatal(err)
	}
	if cap.Limit != maxRetainedJobs || cap.Retained != 0 || cap.Remaining != maxRetainedJobs {
			t.Fatalf("empty capacity %#v", cap)
	}

	fillStoreJobs(t, store, 0, maxRetainedJobs, "client")
	cap, err = store.Capacity()
	if err != nil {
		t.Fatal(err)
	}
	if cap.Retained != maxRetainedJobs || cap.Remaining != 0 {
			t.Fatalf("full capacity %#v", cap)
	}
}

func TestCapacityQueueFullAcrossClients(t *testing.T) {
	store, dir := openTestStore(t)
	first := fillStoreJobs(t, store, 0, maxRetainedJobs, "client")

	overflow, err := store.Submit("client.overflow", sampleSubmission("req.overflow"))
	if err == nil || overflow.ID != "" {
		t.Fatalf("expected queue_full with empty job, got err=%v job=%#v", err, overflow)
	}
	if errorCode(err) != "queue_full" {
		t.Fatalf("expected queue_full, got %v", err)
	}

	cap, err := store.Capacity()
	if err != nil || cap.Retained != maxRetainedJobs {
		t.Fatalf("retained count after overflow: %v %#v", err, cap)
	}

	again, err := store.Submit("client.0", sampleSubmission("req.cap.0"))
	if err != nil || again.ID != first["req.cap.0"].ID {
		t.Fatalf("idempotent submit at capacity failed: %v %#v", err, again)
	}

	changed := sampleSubmission("req.cap.0")
	changed.Messages[0].Content = "different payload"
	_, err = store.Submit("client.0", changed)
	if errorCode(err) != "conflict" {
		t.Fatalf("expected conflict for changed request at capacity, got %v", err)
	}

	reopened, err := Open(dir, testKey)
	if err != nil {
		t.Fatal(err)
	}
	cap2, err := reopened.Capacity()
	if err != nil || cap2 != cap {
		t.Fatalf("capacity changed on reopen: before=%#v after=%#v err=%v", cap, cap2, err)
	}
}

func TestCapacityDisabledDenied(t *testing.T) {
	store, _ := openTestStore(t)
	if err := store.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	_, err := store.Capacity()
	if errorCode(err) != "disabled" {
		t.Fatalf("expected disabled, got %v", err)
	}
}

func TestCapacityEncryptedPersistence(t *testing.T) {
	store, dir := openTestStore(t)
	fillStoreJobs(t, store, 0, 3, "client")
	before, err := store.Capacity()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, stateFileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "req.cap.") {
		t.Fatal("request ids found in plaintext state file")
	}
	reopened, err := Open(dir, testKey)
	if err != nil {
		t.Fatal(err)
	}
	after, err := reopened.Capacity()
	if err != nil || after != before {
		t.Fatalf("reopen capacity mismatch: before=%#v after=%#v err=%v", before, after, err)
	}
}

// Child processes use only temporary fixture state and a fixed, non-secret test key.
func TestCapacityProcessHelper(t *testing.T) {
	dir := os.Getenv("DOTQUEUE_CAPACITY_TEST_DIR")
	if dir == "" {
		return
	}
	store, err := Open(dir, testKey)
	if err != nil {
		t.Fatal(err)
	}
	switch os.Getenv("DOTQUEUE_CAPACITY_TEST_OP") {
	case "submit_new":
		_, e := store.Submit("client.child", sampleSubmission("req.child.overflow"))
		if e != nil {
			fmt.Println("SUBMIT=" + errorCode(e))
		} else {
			fmt.Println("SUBMIT=ok")
		}
	case "capacity":
		c, e := store.Capacity()
		if e != nil {
			fmt.Println("CAPACITY=" + errorCode(e))
		} else {
			fmt.Printf("CAPACITY=%d,%d,%d\n", c.Limit, c.Retained, c.Remaining)
		}
	}
}

func TestCapacityCrossProcessSubmitAtFull(t *testing.T) {
	store, dir := openTestStore(t)
	fillStoreJobs(t, store, 0, maxRetainedJobs, "client")

	cmd := exec.Command(os.Args[0], "-test.run=^TestCapacityProcessHelper$", "-test.v")
	cmd.Env = append(os.Environ(),
		"DOTQUEUE_CAPACITY_TEST_DIR="+dir,
		"DOTQUEUE_CAPACITY_TEST_OP=submit_new",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child failed: %v %s", err, out)
	}
	body := string(out)
	if !strings.Contains(body, "SUBMIT=queue_full") {
		t.Fatalf("expected child queue_full, got %s", body)
	}

	cap, err := store.Capacity()
	if err != nil || cap.Retained != maxRetainedJobs {
		t.Fatalf("parent retained count changed: %v %#v", err, cap)
	}
}

func TestCapacityConcurrentIdempotentAtFull(t *testing.T) {
	store, dir := openTestStore(t)
	sub := sampleSubmission("req.idem.full")
	first, err := store.Submit("client.a", sub)
	if err != nil {
		t.Fatal(err)
	}
	fillStoreJobs(t, store, 1, maxRetainedJobs-1, "client")

	other, err := Open(dir, testKey)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	var jobs [2]Job
	var errs [2]error
	for i, s := range []*Store{store, other} {
		wg.Add(1)
		go func(i int, s *Store) {
			defer wg.Done()
			<-start
			jobs[i], errs[i] = s.Submit("client.a", sub)
		}(i, s)
	}
	close(start)
	wg.Wait()

	for i := 0; i < 2; i++ {
		if errs[i] != nil {
			t.Fatalf("idempotent submit %d failed: %v", i, errs[i])
		}
		if jobs[i].ID != first.ID {
			t.Fatalf("job %d changed id: %s vs %s", i, jobs[i].ID, first.ID)
		}
	}

	cap, err := store.Capacity()
	if err != nil || cap.Retained != maxRetainedJobs {
		t.Fatalf("capacity after concurrent idempotent submit %#v err=%v", cap, err)
	}
}
