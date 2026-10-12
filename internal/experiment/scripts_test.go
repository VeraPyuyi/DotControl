package experiment

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/connections"
)

func TestLinuxFixtureAndIndependentReadback(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux /proc identity required; protocol seam is tested on Windows")
	}
	base := t.TempDir()
	calls := 0
	remote := func(ctx context.Context, _ connections.ServerProfile, r Request) ([]byte, error) {
		calls++
		cmd := exec.CommandContext(ctx, "sh", "-c", r.Script)
		out, e := cmd.Output()
		if e != nil {
			t.Log("script failed", e)
		}
		return out, e
	}
	m, dir := testManager(t, remote)
	_, e := connections.NewStore(filepath.Join(dir, "connections")).UpsertServer(connections.ServerProfile{ID: "fixture", Name: "Fixture", SSHAlias: "example.invalid", RemoteDir: base})
	if e != nil {
		t.Fatal(e)
	}
	s, e := m.Run(context.Background(), "submit", "fixture", "linux-sum")
	for i := 0; i < 40 && s.State != "completed"; i++ {
		time.Sleep(25 * time.Millisecond)
		s, e = m.Run(context.Background(), "status", "fixture", "linux-sum")
	}
	if e != nil || s.State != "completed" || s.Result != ExpectedResult || s.ResultSHA256 != expectedHash() || !s.Expected || calls < 3 {
		t.Fatalf("%+v %v calls=%d", s, e, calls)
	}
	claim, err := os.ReadFile(filepath.Join(base, "dotcontrol-linux-sum", "binding"))
	if err != nil {
		t.Fatal(err)
	}
	s, e = m.Run(context.Background(), "submit", "fixture", "linux-sum")
	again, _ := os.ReadFile(filepath.Join(base, "dotcontrol-linux-sum", "binding"))
	if e != nil || s.State != "completed" || string(claim) != string(again) {
		t.Fatalf("retry %+v %v", s, e)
	}
	// Abnormal evidence is never trusted or followed.
	result := filepath.Join(base, "dotcontrol-linux-sum", "result")
	if e = os.Remove(result); e != nil {
		t.Fatal(e)
	}
	private := filepath.Join(base, "private")
	_ = os.WriteFile(private, []byte(ExpectedResult), 0600)
	if e = os.Symlink(private, result); e != nil {
		t.Fatal(e)
	}
	s, e = m.Run(context.Background(), "status", "fixture", "linux-sum")
	if e != nil || s.State == "completed" || s.Result != "" {
		t.Fatalf("symlink trusted %+v %v", s, e)
	}
}

func TestLinuxRemoteAbnormalPathsAndHardlinks(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux filesystem checks")
	}
	for _, kind := range []string{"symlink-directory", "unsafe-parent", "hardlink-binding"} {
		t.Run(kind, func(t *testing.T) {
			base := t.TempDir()
			target := filepath.Join(base, "dotcontrol-bad")
			switch kind {
			case "symlink-directory":
				_ = os.Symlink(t.TempDir(), target)
			case "unsafe-parent":
				_ = os.Chmod(base, 0777)
			case "hardlink-binding":
				_ = os.Mkdir(target, 0700)
				f := filepath.Join(base, "other")
				_ = os.WriteFile(f, []byte("unused"), 0600)
				_ = os.Link(f, filepath.Join(target, "binding"))
			}
			r := Request{Operation: "submit", ExperimentID: "bad", Binding: strings.Repeat("a", 64), Nonce: strings.Repeat("b", 32)}
			r.Script = remoteScript(base, r)
			output, e := exec.Command("sh", "-c", r.Script).Output()
			if e != nil {
				t.Fatal(e)
			}
			s, ok := decodeStatus(output, r)
			if !ok || s.State != "unsafe_remote_state" {
				t.Fatalf("%s %+v", output, s)
			}
		})
	}
}

func TestScriptsQuoteProfileAndContainNoUserCommand(t *testing.T) {
	r := Request{Operation: "submit", ExperimentID: "fixture", Binding: strings.Repeat("a", 64), Nonce: strings.Repeat("b", 32)}
	script := remoteScript("/tmp/a' $(touch nope)", r)
	if !strings.Contains(script, `'"'"'`) || !strings.Contains(script, "nohup") || !strings.Contains(script, "worker.claim") || !strings.Contains(script, "/proc/") {
		t.Fatal("missing quoting or ownership checks")
	}
	var bash string
	if runtime.GOOS == "windows" {
		bash = `C:\Program Files\Git\bin\bash.exe`
	} else {
		bash = "bash"
	}
	cmd := exec.Command(bash, "-n")
	cmd.Stdin = strings.NewReader(script)
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("invalid shell syntax: %v %s", e, out)
	}
}

func TestLinuxExplicitStopUsesOnlyVerifiedOwnedCooperativeRequest(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux process identity checks")
	}
	base := t.TempDir()
	dir := filepath.Join(base, "dotcontrol-stop-test")
	if e := os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	r := Request{Operation: "stop", ExperimentID: "stop-test", Binding: strings.Repeat("a", 64), Nonce: strings.Repeat("b", 32)}
	if e := os.WriteFile(filepath.Join(dir, "binding"), []byte(r.Binding+"\n"), 0600); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// The local test worker cooperates only through its own request file. It
	// never runs on a server and the helper never receives a kill interface.
	worker := exec.CommandContext(ctx, "sh", "-c", `umask 077; printf '%s\n' "$$" > pid; awk '{print $22}' /proc/$$/stat > starttime; cat /proc/sys/kernel/random/boot_id > boot; while [ ! -f stop.request ]; do sleep 0.01; done; printf '130\n' > exit`)
	worker.Dir = dir
	if e := worker.Start(); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- worker.Wait() }()
	defer func() { cancel(); <-done }()
	for i := 0; i < 100; i++ {
		if b, _ := os.ReadFile(filepath.Join(dir, "boot")); len(b) == 37 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	// A stale identity must not create even a cooperative request.
	startPath := filepath.Join(dir, "starttime")
	start, _ := os.ReadFile(startPath)
	_ = os.WriteFile(startPath, []byte("0\n"), 0600)
	r.Script = remoteScript(base, r)
	output, e := exec.CommandContext(ctx, "sh", "-c", r.Script).Output()
	if e != nil {
		t.Fatal(e)
	}
	s, ok := decodeStatus(output, r)
	if !ok || s.State != "stop_unavailable" {
		t.Fatalf("stale identity %s", output)
	}
	if _, e = os.Stat(filepath.Join(dir, "stop.request")); !os.IsNotExist(e) {
		t.Fatal("stale process received stop request")
	}
	_ = os.WriteFile(startPath, start, 0600)
	output, e = exec.CommandContext(ctx, "sh", "-c", r.Script).Output()
	if e != nil {
		t.Fatal(e)
	}
	s, ok = decodeStatus(output, r)
	if !ok || s.State != "stop_requested" {
		t.Fatalf("owned stop %s", output)
	}
}

func TestLinuxFixtureHonorsCooperativeStop(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux fixture")
	}
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "stop.request"), []byte("stop\n"), 0600)
	cmd := exec.Command("sh", "-c", workerScript)
	cmd.Dir = dir
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("%v %s", e, out)
	}
	exit, _ := os.ReadFile(filepath.Join(dir, "exit"))
	result, _ := os.ReadFile(filepath.Join(dir, "result"))
	if string(exit) != "130\n" || len(result) != 0 {
		t.Fatalf("stop result %q %q", exit, result)
	}
}
