package dotbridge

import (
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/dotqueue"
)

// gateQueue blocks one List call while it owns the store lock, so a concurrent
// transaction contends with an in-flight read.
type gateQueue struct {
	QueueAPI
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (q *gateQueue) List(id string) ([]dotqueue.Job, error) {
	q.once.Do(func() {
		close(q.entered)
		<-q.release
	})
	return q.QueueAPI.List(id)
}

type listOutcome struct {
	code int
	raw  []byte
}

// TestListAPIWaitsForConcurrentTransaction pins the store read path: a read
// that reaches the store while another transaction holds the lock waits for
// it rather than failing with dot_queue_unknown. CI reported that as an
// intermittent 400 from the macOS race run.
func TestListAPIWaitsForConcurrentTransaction(t *testing.T) {
	s, token, _ := openTestBridge(t)
	held := &gateQueue{QueueAPI: s.queue, entered: make(chan struct{}), release: make(chan struct{})}
	s.queue = held
	api := httptest.NewServer(s.Handler())
	defer api.Close()

	listed := make(chan listOutcome, 1)
	go func() {
		code, raw := listHTTPRequest(t, api, "GET", "/dot-bridge/v1/jobs", token, nil)
		listed <- listOutcome{code, raw}
	}()
	<-held.entered

	// The same store, as the service would reach it, for the competing write.
	store := held.QueueAPI.(QueueAPI)
	writeErr := make(chan error, 1)
	go func() { writeErr <- store.SetEnabled(true) }()
	time.Sleep(150 * time.Millisecond)
	close(held.release)

	if err := <-writeErr; err != nil {
		t.Fatalf("concurrent write: %v", err)
	}
	select {
	case got := <-listed:
		if got.code != 200 {
			t.Fatalf("list: %d %s", got.code, got.raw)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("list did not finish")
	}
}
