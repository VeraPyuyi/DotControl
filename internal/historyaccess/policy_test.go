package historyaccess

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

const idA = "aaaaaaaa-1111-4111-8111-111111111111"
const idB = "bbbbbbbb-2222-4222-8222-222222222222"

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil || err.Error() != code {
		t.Fatalf("got %v, want %s", err, code)
	}
}

func TestExplicitInitializationAndRestart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	s := NewStore(dir)
	_, err := s.Load()
	wantCode(t, err, "history_access_missing")
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("read created state directory")
	}
	_, err = s.Apply(0, "all", nil)
	wantCode(t, err, "history_access_missing")
	p, err := s.InitializeAll()
	if err != nil || p.Version != 1 || p.Revision != 1 || p.Mode != "all" || !Allows(p, idA) {
		t.Fatalf("initialize: %+v %v", p, err)
	}
	p, err = s.Apply(p.Revision, "allowlist", []string{idB, idA, idA})
	if err != nil || !reflect.DeepEqual(p.AllowedThreadIDs, []string{idA, idB}) {
		t.Fatalf("apply: %+v %v", p, err)
	}
	restarted := NewStore(dir)
	got, err := restarted.InitializeAll()
	if err != nil || !reflect.DeepEqual(got, p) {
		t.Fatalf("reinitialization changed policy: %+v %v", got, err)
	}
	p, err = restarted.Apply(p.Revision, "allowlist", nil)
	if err != nil || Allows(p, idA) || p.AllowedThreadIDs == nil {
		t.Fatalf("empty must deny: %+v %v", p, err)
	}
	if err := os.Remove(filepath.Join(dir, "history_access.json")); err != nil {
		t.Fatal(err)
	}
	_, err = NewStore(dir).Load()
	wantCode(t, err, "history_access_missing")
	_, err = NewStore(dir).InitializeAll()
	wantCode(t, err, "history_access_missing")
}

func TestPolicyCorruptionNeverResets(t *testing.T) {
	valid := `{"version":1,"mode":"all","revision":1,"allowed_thread_ids":[]}`
	for _, raw := range []string{"", "null", "{}", valid + "{}", strings.Replace(valid, `"version":1`, `"version":2`, 1), strings.Replace(valid, `"revision":1`, `"revision":0`, 1), strings.Replace(valid, `"allowed_thread_ids":[]`, `"allowed_thread_ids":null`, 1), strings.Replace(valid, `"version":1`, `"Version":1`, 1), strings.Replace(valid, `"version":1`, `"version":1,"version":1`, 1), strings.Replace(valid, `"mode":"all"`, `"mode":"other"`, 1), strings.Replace(valid, `"allowed_thread_ids":[]`, `"allowed_thread_ids":["../private"]`, 1), strings.Replace(valid, `"allowed_thread_ids":[]`, `"allowed_thread_ids":["`+idA+`"]`, 1), strings.Repeat(" ", 1024*1024+1)} {
		t.Run("invalid", func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "history_access.json")
			if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			s := NewStore(dir)
			_, err := s.Load()
			wantCode(t, err, "history_invalid_policy")
			_, err = s.InitializeAll()
			wantCode(t, err, "history_invalid_policy")
			_, err = s.Apply(1, "all", nil)
			wantCode(t, err, "history_invalid_policy")
			got, _ := os.ReadFile(path)
			if string(got) != raw {
				t.Fatal("corrupt file was replaced")
			}
		})
	}
}

func TestValidationFingerprintAndCAS(t *testing.T) {
	for _, id := range []string{"", "client-new-thread:" + idA, "client-" + idA, "temporary-" + idA, "../" + idA, "C:\\" + idA, strings.ToUpper(idA), idA + "\n", "thread-one"} {
		if ValidateThreadID(id) {
			t.Fatalf("accepted %q", id)
		}
	}
	if !ValidateThreadID(idA) {
		t.Fatal("stable UUID rejected")
	}
	s := NewStore(t.TempDir())
	p, err := s.InitializeAll()
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Apply(0, "allowlist", nil)
	wantCode(t, err, "history_revision_conflict")
	_, err = s.Apply(p.Revision, "unknown", nil)
	wantCode(t, err, "history_invalid_policy")
	_, err = s.Apply(p.Revision, "all", []string{idA})
	wantCode(t, err, "history_invalid_policy")
	_, err = s.Apply(p.Revision, "allowlist", []string{"client-new-thread:" + idA})
	wantCode(t, err, "history_invalid_policy")
	_, err = s.Apply(p.Revision, "allowlist", make([]string, 10001))
	wantCode(t, err, "history_invalid_policy")
	a := Policy{Version: 1, Mode: "allowlist", Revision: 2, AllowedThreadIDs: []string{idA, idB, idA}}
	b := a
	b.AllowedThreadIDs = []string{idB, idA}
	if Fingerprint(a) == "" || Fingerprint(a) != Fingerprint(b) {
		t.Fatal("fingerprint not normalized")
	}
	b.Revision++
	if Fingerprint(a) == Fingerprint(b) {
		t.Fatal("fingerprint omitted revision")
	}
	b = a
	b.AllowedThreadIDs = []string{idB}
	if Fingerprint(a) == Fingerprint(b) {
		t.Fatal("fingerprint omitted IDs")
	}
	if Allows(Policy{Mode: "all"}, idA) || Allows(p, "../bad") {
		t.Fatal("invalid policy/ID granted access")
	}
	before, _ := s.Load()
	after, _ := s.Load()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("failed update changed policy")
	}
}

func TestConcurrentCASAcrossStoreInstances(t *testing.T) {
	dir := t.TempDir()
	p, err := NewStore(dir).InitializeAll()
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 12)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := NewStore(dir).Apply(p.Revision, "allowlist", []string{idA})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else {
			wantCode(t, err, "history_revision_conflict")
		}
	}
	if success != 1 {
		t.Fatalf("successes=%d", success)
	}
	got, err := NewStore(dir).Load()
	if err != nil || got.Revision != 2 {
		t.Fatalf("final: %+v %v", got, err)
	}
}

func TestCrossProcessCAS(t *testing.T) {
	if dir := os.Getenv("DOTCONTROL_HISTORY_TEST_DIR"); dir != "" {
		_, err := NewStore(dir).Apply(1, "allowlist", []string{idA})
		if err == nil {
			os.Exit(0)
		}
		if err.Error() == "history_revision_conflict" {
			os.Exit(3)
		}
		os.Exit(4)
	}
	dir := t.TempDir()
	if _, err := NewStore(dir).InitializeAll(); err != nil {
		t.Fatal(err)
	}
	var cmds []*exec.Cmd
	for i := 0; i < 4; i++ {
		cmd := exec.Command(os.Args[0], "-test.run=^TestCrossProcessCAS$")
		cmd.Env = append(os.Environ(), "DOTCONTROL_HISTORY_TEST_DIR="+dir)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		cmds = append(cmds, cmd)
	}
	success := 0
	for _, cmd := range cmds {
		err := cmd.Wait()
		if err == nil {
			success++
		} else if e, ok := err.(*exec.ExitError); !ok || e.ExitCode() != 3 {
			t.Fatalf("child: %v", err)
		}
	}
	if success != 1 {
		t.Fatalf("successes=%d", success)
	}
	data, err := os.ReadFile(filepath.Join(dir, "history_access.json"))
	if err != nil {
		t.Fatal(err)
	}
	var p Policy
	if json.Unmarshal(data, &p) != nil || p.Revision != 2 || p.Mode != "allowlist" {
		t.Fatalf("bad persisted CAS: %s", data)
	}
}

func TestLinkedStoreFilesAreRejected(t *testing.T) {
	for _, name := range []string{"history_access.json", "history_access.lock", "history_access.initialized"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			s := NewStore(dir)
			if _, err := s.InitializeAll(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, name)
			if err := os.Link(path, filepath.Join(dir, "hardlinked")); err != nil {
				t.Skipf("hard links unavailable: %v", err)
			}
			_, err := s.InitializeAll()
			wantCode(t, err, "history_storage_unavailable")
		})
	}
}

func TestRevisionOverflowPreservesPolicy(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	p, err := s.InitializeAll()
	if err != nil {
		t.Fatal(err)
	}
	p.Revision = ^uint64(0)
	b, _ := json.Marshal(p)
	if err = os.WriteFile(filepath.Join(dir, "history_access.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	_, err = s.Apply(p.Revision, "allowlist", nil)
	wantCode(t, err, "history_revision_exhausted")
	got, err := s.Load()
	if err != nil || !reflect.DeepEqual(got, p) {
		t.Fatalf("overflow changed policy: %+v %v", got, err)
	}
}
