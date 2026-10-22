package dotbridge

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dotcontrol/dotcontrol/internal/dotqueue"
)

func TestJobSummarySurfacesOnGetListAndRequest(t *testing.T) {
	s, token, clientID := openTestBridge(t)
	api := httptest.NewServer(s.Handler())
	defer api.Close()

	job, err := s.queue.Submit(clientID, dotqueue.Submission{
		RequestID: "summary.req",
		SessionID: "session.summary",
		Mode:      dotqueue.ModeAnswer,
		Messages:  []dotqueue.Message{{Role: "user", Content: "work"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimTask(clientID, job.ID, "worker.summary")
	if err != nil {
		t.Fatal(err)
	}
	summary := "waiting for operator confirmation"
	if _, err = s.ProgressTask(job.ID, claim.Token, dotqueue.StateWaitingUser, summary); err != nil {
		t.Fatal(err)
	}

	paths := []string{
		"/dot-bridge/v1/jobs/" + job.ID,
		"/dot-bridge/v1/requests/" + job.RequestID,
	}
	for _, path := range paths {
		code, raw := listHTTPRequest(t, api, http.MethodGet, path, token, nil)
		var dto JobDTO
		if code != http.StatusOK || json.Unmarshal(raw, &dto) != nil || dto.Summary != summary || dto.State != dotqueue.StateWaitingUser {
			t.Fatalf("%s: %d %#v %s", path, code, dto, raw)
		}
	}

	code, raw := listHTTPRequest(t, api, http.MethodGet, "/dot-bridge/v1/jobs", token, nil)
	var list JobListDTO
	if code != http.StatusOK || json.Unmarshal(raw, &list) != nil || len(list.Jobs) != 1 {
		t.Fatalf("list: %d %s", code, raw)
	}
	if list.Jobs[0].Summary != summary {
		t.Fatalf("list summary %#v", list.Jobs[0])
	}
}

func TestJobSummaryProgressToJobDTO(t *testing.T) {
	s, _, clientID := openTestBridge(t)
	job, err := s.queue.Submit(clientID, dotqueue.Submission{
		RequestID: "dto.req",
		SessionID: "session.dto",
		Mode:      dotqueue.ModeAnswer,
		Messages:  []dotqueue.Message{{Role: "user", Content: "x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimTask(clientID, job.ID, "worker.dto")
	if err != nil {
		t.Fatal(err)
	}
	want := "running step 2 of 5"
	updated, err := s.ProgressTask(job.ID, claim.Token, dotqueue.StateRunning, want)
	if err != nil {
		t.Fatal(err)
	}
	dto := toJobDTO(updated)
	if dto.Summary != want {
		t.Fatalf("toJobDTO summary %q", dto.Summary)
	}
	s.mu.Lock()
	policy := s.state.Clients[clientID]
	s.mu.Unlock()
	got, err := s.getJob(policy, job.ID)
	if err != nil || got.Summary != want {
		t.Fatalf("getJob %#v err=%v", got, err)
	}
}
