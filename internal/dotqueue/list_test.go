package dotqueue

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestListRejectsInvalidClientID(t *testing.T) {
	store, _ := openTestStore(t)
	jobs, err := store.List("")
	if err == nil || jobs != nil {
		t.Fatalf("expected invalid client rejection, got jobs=%#v err=%v", jobs, err)
	}
	if errorCode(err) != "unknown" {
		t.Fatalf("expected unknown, got %v", err)
	}
}

func TestListDisabledFailsClosed(t *testing.T) {
	store, _ := openTestStore(t)
	if _, err := store.Submit("client.a", sampleSubmission("req.list.disabled")); err != nil {
		t.Fatal(err)
	}
	if err := store.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	jobs, err := store.List("client.a")
	if err == nil || jobs != nil {
		t.Fatalf("expected disabled list failure, got jobs=%#v err=%v", jobs, err)
	}
	if errorCode(err) != "disabled" {
		t.Fatalf("expected disabled, got %v", err)
	}
}

func TestListOwnerForeignAndZero(t *testing.T) {
	store, _ := openTestStore(t)
	zero, err := store.List("client.empty")
	if err != nil {
		t.Fatal(err)
	}
	if zero == nil || len(zero) != 0 {
		t.Fatalf("expected empty owner list, got %#v", zero)
	}

	first, err := store.Submit("client.a", sampleSubmission("req.list.owner.1"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Submit("client.a", sampleSubmission("req.list.owner.2"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Submit("client.b", sampleSubmission("req.list.foreign")); err != nil {
		t.Fatal(err)
	}

	listed, err := store.List("client.a")
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 {
		t.Fatalf("expected two owner jobs, got %#v", listed)
	}
	if listed[0].ID != second.ID || listed[1].ID != first.ID {
		t.Fatalf("expected newest-first order, got %#v", listed)
	}
	for _, job := range listed {
		if job.ClientID != "client.a" {
			t.Fatalf("foreign job leaked: %#v", job)
		}
	}
}

func TestListIncludesTerminalStatesAndStableReopen(t *testing.T) {
	store, dir := openTestStore(t)
	queued, err := store.Submit("client.a", sampleSubmission("req.list.cancelled"))
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := store.Cancel("client.a", queued.ID)
	if err != nil || cancelled.State != StateCancelled {
		t.Fatalf("cancel failed: %v %#v", err, cancelled)
	}

	active, err := store.Submit("client.a", sampleSubmission("req.list.completed"))
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.Claim("client.a", active.ID, "worker.1")
	if err != nil {
		t.Fatal(err)
	}
	done, err := store.Complete(active.ID, claim.Token, Result{Text: "ok"})
	if err != nil || done.State != StateCompleted {
		t.Fatalf("complete failed: %v %#v", err, done)
	}

	before, err := store.List("client.a")
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 2 {
		t.Fatalf("expected cancelled and completed jobs, got %#v", before)
	}
	states := map[string]bool{before[0].State: true, before[1].State: true}
	if !states[StateCancelled] || !states[StateCompleted] {
		t.Fatalf("missing terminal states: %#v", before)
	}

	reopened, err := Open(dir, testKey)
	if err != nil {
		t.Fatal(err)
	}
	after, err := reopened.List("client.a")
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("reopen changed list length: before=%d after=%d", len(before), len(after))
	}
	for i := range before {
		if after[i].ID != before[i].ID || after[i].State != before[i].State {
			t.Fatalf("reopen changed order or identity: before=%#v after=%#v", before, after)
		}
	}
}

func TestListOmitsSensitiveFieldsAndDoesNotAlias(t *testing.T) {
	store, _ := openTestStore(t)
	sub := sampleSubmission("req.list.sensitive")
	sub.Messages[0].Content = "secret prompt body"
	job, err := store.Submit("client.a", sub)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.Claim("client.a", job.ID, "worker.1")
	if err != nil || claim.Token == "" {
		t.Fatalf("claim failed: %v %#v", err, claim)
	}
	completed, err := store.Complete(job.ID, claim.Token, Result{
		Text:      "result text",
		Artifacts: []Artifact{{URL: "https://example.com/a", Label: "a"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	listed, err := store.List("client.a")
	if err != nil || len(listed) != 1 {
		t.Fatalf("list failed: %v %#v", err, listed)
	}
	payload, err := json.Marshal(listed)
	if err != nil {
		t.Fatal(err)
	}
	body := string(payload)
	for _, forbidden := range []string{"token", "claim_token", "messages", "task", "secret prompt body", claim.Token} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("serialized list leaked %q in %s", forbidden, body)
		}
	}
	if listed[0].Result == nil || listed[0].Result.Text != completed.Result.Text {
		t.Fatalf("expected public result, got %#v", listed[0])
	}

	listed[0].Summary = "mutated summary"
	listed[0].Result.Text = "mutated result"
	again, err := store.List("client.a")
	if err != nil {
		t.Fatal(err)
	}
	if again[0].Summary != completed.Summary || again[0].Result.Text != completed.Result.Text {
		t.Fatalf("list results alias internal state: %#v", again[0])
	}
}
