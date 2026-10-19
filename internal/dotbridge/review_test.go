package dotbridge

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/dotcontrol/dotcontrol/internal/dotqueue"
)

type interceptQueue struct {
	QueueAPI
	after func()
}

func (q interceptQueue) Submit(id string, sub dotqueue.Submission) (dotqueue.Job, error) {
	j, e := q.QueueAPI.Submit(id, sub)
	q.after()
	return j, e
}
func (q interceptQueue) Get(id, job string) (dotqueue.Job, error) {
	j, e := q.QueueAPI.Get(id, job)
	q.after()
	return j, e
}

func TestReviewCancelRoute(t *testing.T) {
	s, token, id := openTestBridge(t)
	p, e := s.StartProbe(id)
	if e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest(http.MethodPost, "/dot-bridge/v1/jobs/"+p.JobID+"/cancel", nil)
	r.Host = "127.0.0.1:9317"
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("cancel: %d %s", w.Code, w.Body.String())
	}
}
func TestReviewProbeCannotUndoRevoke(t *testing.T) {
	s, _, id := openTestBridge(t)
	s.queue = interceptQueue{QueueAPI: s.queue, after: func() {
		if e := s.RevokeClient(id); e != nil {
			t.Fatal(e)
		}
	}}
	if _, e := s.StartProbe(id); e == nil {
		t.Fatal("probe reported success after revocation")
	}
	s.mu.Lock()
	revoked := s.state.Clients[id].Revoked
	s.mu.Unlock()
	if !revoked {
		t.Fatal("probe resurrected revoked policy")
	}
}
func TestReviewReadRechecksPause(t *testing.T) {
	s, _, id := openTestBridge(t)
	p, e := s.StartProbe(id)
	if e != nil {
		t.Fatal(e)
	}
	var paused atomic.Bool
	s.paused = paused.Load
	s.queue = interceptQueue{QueueAPI: s.queue, after: func() { paused.Store(true) }}
	if _, e := s.getJob(ClientPolicy{ClientID: id}, p.JobID); e == nil {
		t.Fatal("read succeeded after pause during I/O")
	}
}
func TestReviewStrictSubmit(t *testing.T) {
	if _, e := decodeSubmit(strings.NewReader(`{"request_id":"r","mode":"answer","session_id":"s","messages":[{"role":"user","content":"hi"}]}`)); e != nil {
		t.Fatal(e)
	}
}
