package dotbridge

import (
	"github.com/dotcontrol/dotcontrol/internal/dotevents"
	"github.com/dotcontrol/dotcontrol/internal/dotqueue"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProbeCompletionReplayCannotExtendExpiry(t *testing.T) {
	for _, mode := range []string{dotqueue.ModeAnswer, dotqueue.ModeTask} {
		t.Run(mode, func(t *testing.T) { testProbeReplay(t, mode) })
	}
}

func testProbeReplay(t *testing.T, mode string) {
	s, _, id := openTestBridge(t)
	p, err := s.StartProbeMode(id, mode)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.ClaimTask(id, p.JobID, "replay")
	if err != nil {
		t.Fatal(err)
	}
	nonce := ""
	if mode == dotqueue.ModeTask {
		fields := strings.Fields(c.Submission.Task)
		nonce = fields[len(fields)-1]
	} else {
		nonce = strings.TrimPrefix(c.Submission.Messages[0].Content, "Reply with exactly this nonce: ")
	}
	r := dotqueue.Result{Text: nonce}
	if _, err = s.CompleteTask(p.JobID, c.Token, r); err != nil {
		t.Fatal(err)
	}
	past := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	s.mu.Lock()
	policy := s.state.Clients[id]
	proof := policy.proof(mode)
	proof.Verified = past
	proof.Expires = past.Add(time.Minute)
	// Exercise fail-closed legacy proof: no durable binding evidence.
	proof.TrustEpoch = ""
	policy.setProof(mode, proof)
	s.state.Clients[id] = policy
	if err = s.saveState(); err != nil {
		t.Fatal(err)
	}
	// Reload durable metadata while retaining the authenticated in-memory binding.
	if err = s.loadState(); err != nil {
		t.Fatal(err)
	}
	s.mu.Unlock()
	if _, err = s.CompleteTask(p.JobID, c.Token, r); err != nil {
		t.Fatal(err)
	}
	status, _ := s.StatusForClient(id)
	if status.Ready {
		t.Fatal("identical completion replay extended expired proof")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if got := s.state.Clients[id].proof(mode); got != proof {
		t.Fatalf("completion replay changed persisted proof: got %#v, want %#v", got, proof)
	}
}

func TestBridgeStateServiceRejectsLinkedMetadata(t *testing.T) {
	s, _, _ := openTestBridge(t)
	path := filepath.Join(s.root, "bridge.state.json")
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte(`{"version":1,"clients":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(target, path); err != nil {
		t.Fatal(err)
	}
	if err := s.loadState(); err == nil {
		t.Fatal("service read followed linked authorization metadata")
	}
	if err := s.saveState(); err == nil {
		t.Fatal("service write followed linked authorization metadata")
	}
}

type wrongModeCompletion struct{ QueueAPI }

func (q wrongModeCompletion) Complete(id, token string, result dotqueue.Result) (dotqueue.Job, error) {
	job, err := q.QueueAPI.Complete(id, token, result)
	job.Mode = dotqueue.ModeAnswer
	return job, err
}
func TestTaskProbeRejectsCompletionModeMismatch(t *testing.T) {
	s, _, id := openTestBridge(t)
	p, err := s.StartProbeMode(id, dotqueue.ModeTask)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.ClaimTask(id, p.JobID, "mode")
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(c.Submission.Task)
	s.queue = wrongModeCompletion{QueueAPI: s.queue}
	if _, err = s.CompleteTask(p.JobID, c.Token, dotqueue.Result{Text: fields[len(fields)-1]}); err != nil {
		t.Fatal(err)
	}
	status, _ := s.StatusForClient(id)
	if status.Ready {
		t.Fatal("mismatched job mode proved capability")
	}
}

type renewingStatusEvents struct {
	EventsAPI
	renew func()
}

func (e renewingStatusEvents) Status(a, b string) (dotevents.Status, error) {
	e.renew()
	return e.EventsAPI.Status(a, b)
}
func TestProbeStatusRenewalCannotMixGenerations(t *testing.T) {
	s, _, id := openTestBridge(t)
	p, err := s.StartProbe(id)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.ClaimTask(id, p.JobID, "status")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CompleteTask(p.JobID, c.Token, dotqueue.Result{Text: c.Submission.Messages[0].Content[len("Reply with exactly this nonce: "):]}); err != nil {
		t.Fatal(err)
	}
	s.events = renewingStatusEvents{EventsAPI: s.events, renew: func() { VerifyTestSubscription(t, s, id) }}
	status, _ := s.StatusForClient(id)
	if !status.Ready {
		t.Fatal("continuous same-binding renewal lost verified proof")
	}
}
