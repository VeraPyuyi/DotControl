package codexbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotcontrol/dotcontrol/internal/model"
)

func resultFixture(t *testing.T) (*Bridge, Config, model.CodexSubmit, model.CodexJob) {
	t.Helper()
	cfg, req := runtimeFixture(t, &runtimePeer{})
	cfg.Runtime.Acceptance = nil // Collect events without dispatching any real turn.
	b, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	j, err := b.Submit(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return b, cfg, req, j
}

func collectResult(t *testing.T, b *Bridge, id string, item any) {
	t.Helper()
	raw, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	b.collectItem(id, raw)
}

func TestResultAfterEvidenceCapAndDuplicateFinalEvent(t *testing.T) {
	b, cfg, req, job := resultFixture(t)
	for i := 0; i < 128; i++ {
		collectResult(t, b, job.JobID, map[string]any{"id": fmt.Sprint(i), "type": "commandExecution", "exitCode": 0})
	}
	item := map[string]any{"id": "final", "type": "agentMessage", "text": "最终实际结果"}
	collectResult(t, b, job.JobID, item)
	// The same item can occur both individually and in turn/completed.
	collectResult(t, b, job.JobID, item)
	b.finishTurn(job.JobID, "completed")
	j, err := b.Job(req.DeviceID, job.JobID)
	if err != nil || j.Runtime.Text != "最终实际结果" || !j.Runtime.EvidenceTruncated || j.Runtime.TextTruncated || len(j.Runtime.Evidence) != 128 || j.State != "completed" {
		t.Fatalf("%+v %v", j.Runtime, err)
	}
	journal, err := os.ReadFile(filepath.Join(cfg.StateDir, "codex-runtime-v1.jsonl"))
	if err != nil || strings.Contains(string(journal), "最终实际结果") {
		t.Fatal("result body persisted", err)
	}
}

func TestResultTruncationBudgetsAndChineseBoundary(t *testing.T) {
	b, _, req, job := resultFixture(t)
	for i := 0; i < 9; i++ {
		collectResult(t, b, job.JobID, map[string]any{"id": fmt.Sprint(i), "type": "commandExecution", "aggregatedOutput": strings.Repeat("x", 8193)})
	}
	collectResult(t, b, job.JobID, map[string]any{"id": "text", "type": "agentMessage", "text": strings.Repeat("文", maxResultText)})
	j, _ := b.Job(req.DeviceID, job.JobID)
	if !j.Runtime.TextTruncated || len(j.Runtime.Text) > maxResultText {
		t.Fatal("missing text truncation", j.Runtime)
	}
	for i, e := range j.Runtime.Evidence[:9] {
		want := 8192
		if i == 8 {
			want = 0
		}
		if len(e.Output) != want || !e.OutputTruncated {
			t.Fatalf("evidence %d: %+v", i, e)
		}
	}
	for _, tc := range []struct {
		limit int
		want  string
	}{{0, ""}, {1, ""}, {3, "你"}, {4, "你"}, {6, "你好"}} {
		if got := boundedText("你好a", tc.limit); got != tc.want {
			t.Fatalf("limit=%d got=%q", tc.limit, got)
		}
	}
}

func TestMCPOutputTruncationIsReported(t *testing.T) {
	b, _, req, job := resultFixture(t)
	collectResult(t, b, job.JobID, map[string]any{"id": "mcp", "type": "mcpToolCall", "result": map[string]any{"content": []any{map[string]any{"type": "text", "text": strings.Repeat("a", 8192)}, map[string]any{"type": "text", "text": "omitted"}}}})
	j, _ := b.Job(req.DeviceID, job.JobID)
	if len(j.Runtime.Evidence) != 1 || !j.Runtime.Evidence[0].OutputTruncated || len(j.Runtime.Evidence[0].Output) != 8192 {
		t.Fatalf("%+v", j.Runtime)
	}
}

func TestDiscardedResultEventsDoNotGrowJournal(t *testing.T) {
	b, cfg, req, job := resultFixture(t)
	for i := 0; i < 129; i++ {
		collectResult(t, b, job.JobID, map[string]any{"id": fmt.Sprint(i), "type": "commandExecution", "exitCode": 0})
	}
	path := filepath.Join(cfg.StateDir, "codex-runtime-v1.jsonl")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		for _, kind := range []string{"commandExecution", "fileChange"} {
			collectResult(t, b, job.JobID, map[string]any{"id": fmt.Sprint(i), "type": kind, "status": "completed"})
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("discarded event appended journal", err)
	}
	final := map[string]any{"id": "final-result", "type": "agentMessage", "text": "retained result"}
	collectResult(t, b, job.JobID, final)
	before, _ = os.ReadFile(path)
	collectResult(t, b, job.JobID, final)
	after, _ = os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("replayed final appended journal")
	}
	b.finishTurn(job.JobID, "completed")
	j, _ := b.Job(req.DeviceID, job.JobID)
	if j.State != "completed" || j.Runtime.Text != "retained result" {
		t.Fatalf("%+v", j)
	}
}

func TestRetiredCompletedResultUnavailableAfterRestart(t *testing.T) {
	b, cfg, req, job := resultFixture(t)
	b.mu.Lock()
	j := b.runtime.jobs[job.JobID]
	j.entry.Job.ServerProfileID = "legacy-server"
	j.entry.Job.State = "completed"
	j.entry.Job.Runtime.ResultStatus = "available"
	j.entry.Job.Runtime.ImageDeliveryStatus = "received_in_band"
	j.entry.HadResults = true
	if err := b.saveRuntimeLocked(j); err != nil {
		t.Fatal(err)
	}
	b.mu.Unlock()
	b.Close()
	path := filepath.Join(cfg.StateDir, "codex-runtime-v1.jsonl")
	before, _ := os.ReadFile(path)
	cfg.Runtime.StartClient = func(context.Context, string, string) (*Client, error) {
		t.Fatal("retired result dispatched")
		return nil, nil
	}
	reopened, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for _, read := range []func() (model.CodexJob, error){func() (model.CodexJob, error) { return reopened.Job(req.DeviceID, job.JobID) }, func() (model.CodexJob, error) { return reopened.Submit(context.Background(), req) }} {
		v, e := read()
		if e != nil || v.State != "completed" || v.Runtime.ResultStatus != "unavailable_after_restart" || v.Runtime.ImageDeliveryStatus != "unavailable_after_restart" || v.Runtime.Text != "" || len(v.Runtime.Evidence) != 0 || len(v.Runtime.Images) != 0 {
			t.Fatalf("%+v %v", v, e)
		}
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("restoration rewrote retired journal")
	}
}
