package codexbridge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/dotcontrol/dotcontrol/internal/model"
)

func request() model.CodexSubmit {
	return model.CodexSubmit{DeviceID: "device-a", RequestID: "request-a", Prompt: "private prompt 你好", AllowedApps: []string{"Calculator"}, WorkingDirs: []string{os.TempDir()}}
}

func TestDurableDedupeConflictPrivacyAndOwnership(t *testing.T) {
	dir := t.TempDir()
	b, err := New(Config{DeviceID: "device-a", StateDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	r := request()
	first, err := b.Submit(context.Background(), r)
	if err != nil || first.State != "blocked" || first.Code != "official_cu_acceptance_required" || first.JobID == "" {
		t.Fatalf("submit=%+v err=%v", first, err)
	}
	var wg sync.WaitGroup
	for range 24 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, e := b.Submit(context.Background(), r)
			if e != nil || got.JobID != first.JobID {
				t.Errorf("dedupe=%+v %v", got, e)
			}
		}()
	}
	wg.Wait()
	r.Prompt += " changed"
	if _, err = b.Submit(context.Background(), r); err == nil || err.Error() != "request_id_conflict" {
		t.Fatalf("conflict=%v", err)
	}
	if _, err = b.Job("other-device", first.JobID); err == nil || err.Error() != "wrong_device" {
		t.Fatalf("ownership=%v", err)
	}
	if _, err = b.Cancel("device-a", "foreign-thread-id"); err == nil || err.Error() != "job_not_found" {
		t.Fatalf("foreign cancel=%v", err)
	}
	if err = b.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "codex-jobs.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private prompt") || strings.Contains(string(data), "Calculator") || strings.Contains(string(data), os.TempDir()) {
		t.Fatal("private input persisted")
	}
	b, err = New(Config{DeviceID: "device-a", StateDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	got, err := b.Submit(context.Background(), request())
	if err != nil || got != first {
		t.Fatalf("restart=%+v %v", got, err)
	}
	got, err = b.Cancel("device-a", first.JobID)
	if err != nil || got.State != "blocked" || !got.CancelRequested {
		t.Fatalf("terminal cancel=%+v %v", got, err)
	}
	if err = b.Close(); err != nil {
		t.Fatal(err)
	}
	b, err = New(Config{DeviceID: "device-a", StateDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if resumed, e := b.Job("device-a", first.JobID); e != nil || resumed != got {
		t.Fatalf("cancel not durable=%+v %v", resumed, e)
	}
}

func TestRejectedJobPersistsAndStoreLocks(t *testing.T) {
	dir := t.TempDir()
	b, err := New(Config{DeviceID: "device-a", StateDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if other, err := New(Config{DeviceID: "device-a", StateDir: dir}); err == nil {
		other.Close()
		t.Fatal("second store acquired lock")
	}
	r := request()
	r.Prompt = ""
	j, err := b.Submit(context.Background(), r)
	if err != nil || j.State != "rejected" || j.Code != "invalid_prompt" {
		t.Fatalf("rejected=%+v %v", j, err)
	}
	got, err := b.Job("device-a", j.JobID)
	if err != nil || got != j {
		t.Fatalf("query=%+v %v", got, err)
	}
	cap := b.Capabilities()
	if cap.CodexDelegation.Available || cap.OfficialComputerUse.Available {
		t.Fatal("unaccepted native execution advertised")
	}
}

func TestCorruptStoreFailsClosed(t *testing.T) {
	dir := t.TempDir()
	b, err := New(Config{DeviceID: "device-a", StateDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	b.Submit(context.Background(), request())
	b.Close()
	f, err := os.OpenFile(filepath.Join(dir, "codex-jobs.jsonl"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("{partial")
	f.Close()
	if b, err = New(Config{DeviceID: "device-a", StateDir: dir}); err == nil {
		b.Close()
		t.Fatal("corrupt store accepted")
	}
}

func TestPromptHashesAreKeyedPerStoreAndDeviceRejectsBeforeAllocation(t *testing.T) {
	var hashes []string
	for range 2 {
		b, err := New(Config{DeviceID: "device-a", StateDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		r := request()
		r.DeviceID = "foreign-device"
		if _, err = b.Submit(context.Background(), r); err == nil || err.Error() != "wrong_device" {
			t.Fatalf("foreign submit=%v", err)
		}
		if len(b.jobs) != 0 {
			t.Fatal("foreign request allocated job")
		}
		r = request()
		j, err := b.Submit(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		hashes = append(hashes, j.PromptHash)
		b.Close()
	}
	if hashes[0] == hashes[1] {
		t.Fatal("prompt hashes are linkable across stores")
	}
}
