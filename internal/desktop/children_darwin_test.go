//go:build darwin

package desktop

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/sys/unix"
)

func TestMain(m *testing.M) {
	if handled, e := RunChildGuardian(os.Args[1:]); handled {
		if e != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type childFixture struct {
	Parent   int   `json:"parent"`
	Guardian int   `json:"guardian"`
	Children []int `json:"children"`
}

// Every subprocess is a synthetic test process. No native computer-use driver,
// permission prompt, real tunnel or existing daemon is touched.
func TestMacOwnedChildrenExitWithParent(t *testing.T) {
	for _, mode := range []string{"graceful", "parent_sigkill", "guardian_sigkill"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			ready := filepath.Join(dir, "ready.json")
			parent := exec.Command(os.Args[0], "-test.run=^TestMacChildrenHelper$", "--", "parent", ready)
			if mode != "parent_sigkill" {
				parent.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			}
			control, e := parent.StdinPipe()
			if e != nil {
				t.Fatal(e)
			}
			parent.Stderr = os.Stderr
			parent.Stdout = os.Stderr // surface subprocess assertion failures in CI
			if e = parent.Start(); e != nil {
				t.Fatal(e)
			}
			wait := make(chan error, 1)
			go func() { wait <- parent.Wait() }()
			finished := false
			defer func() {
				control.Close()
				if !finished {
					_ = unix.Kill(-parent.Process.Pid, unix.SIGKILL)
				}
			}()
			var fixture childFixture
			deadline := time.Now().Add(10 * time.Second)
			for {
				data, e := os.ReadFile(ready)
				if e == nil && json.Unmarshal(data, &fixture) == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("supervised fixture did not become ready")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if fixture.Parent != parent.Process.Pid || fixture.Guardian <= 1 || len(fixture.Children) != 3 {
				t.Fatalf("invalid fixture %#v", fixture)
			}
			for _, pid := range append(fixture.Children, fixture.Guardian) {
				pgid, e := unix.Getpgid(pid)
				if e != nil || pgid != fixture.Parent {
					t.Fatalf("process %d escaped owned group: %d %v", pid, pgid, e)
				}
			}
			// A preexisting unrelated daemon belongs to a different group and must
			// survive both normal cleanup and the parent's abrupt SIGKILL.
			unrelated := exec.Command(os.Args[0], "-test.run=^TestMacChildrenHelper$", "--", "idle")
			unrelated.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			if e = unrelated.Start(); e != nil {
				t.Fatal(e)
			}
			defer func() { _ = unrelated.Process.Kill(); _ = unrelated.Wait() }()
			kq, e := unix.Kqueue()
			if e != nil {
				t.Fatal(e)
			}
			defer unix.Close(kq)
			remaining := map[uint64]bool{}
			var changes []unix.Kevent_t
			for _, pid := range append(fixture.Children, fixture.Guardian) {
				remaining[uint64(pid)] = true
				changes = append(changes, unix.Kevent_t{Ident: uint64(pid), Filter: unix.EVFILT_PROC, Flags: unix.EV_ADD | unix.EV_ENABLE | unix.EV_ONESHOT, Fflags: unix.NOTE_EXIT})
			}
			if _, e = unix.Kevent(kq, changes, nil, nil); e != nil {
				t.Fatal(e)
			}
			switch mode {
			case "graceful":
				_, e = control.Write([]byte{1})
			case "parent_sigkill":
				e = parent.Process.Kill()
			case "guardian_sigkill":
				e = unix.Kill(fixture.Guardian, unix.SIGKILL)
			}
			if e != nil {
				t.Fatal(e)
			}
			select {
			case err := <-wait:
				if mode == "graceful" && err != nil {
					t.Fatalf("normal parent exit: %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("owned parent did not terminate")
			}
			deadline = time.Now().Add(10 * time.Second)
			for len(remaining) > 0 && time.Now().Before(deadline) {
				events := make([]unix.Kevent_t, 8)
				timeout := unix.NsecToTimespec(int64(100 * time.Millisecond))
				n, e := unix.Kevent(kq, nil, events, &timeout)
				if e == unix.EINTR {
					continue
				}
				if e != nil {
					t.Fatal(e)
				}
				for _, event := range events[:n] {
					if event.Fflags&unix.NOTE_EXIT != 0 {
						delete(remaining, event.Ident)
					}
				}
			}
			if len(remaining) != 0 {
				t.Fatalf("owned processes survived %s: %#v", mode, remaining)
			}
			finished = true
			if mode == "graceful" {
				if data, e := os.ReadFile(ready + ".closed"); e != nil || string(data) != "context-and-mcp-closed" {
					t.Fatalf("normal transport close was not completed: %q %v", data, e)
				}
			}
			if e = unix.Kill(unrelated.Process.Pid, 0); e != nil {
				t.Fatalf("unrelated process was killed: %v", e)
			}
		})
	}
}

func TestMacChildrenHelper(t *testing.T) {
	index := -1
	for i, a := range os.Args {
		if a == "--" {
			index = i
			break
		}
	}
	if index < 0 {
		return
	}
	args := os.Args[index+1:]
	if len(args) == 0 {
		os.Exit(2)
	}
	switch args[0] {
	case "idle":
		for {
			time.Sleep(time.Hour)
		}
	case "stdin":
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	case "parent":
		if len(args) != 2 {
			os.Exit(2)
		}
		if e := OwnChildren(); e != nil {
			t.Fatal(e)
		}
		ctx, cancel := context.WithCancel(context.Background())
		tunnel := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMacChildrenHelper$", "--", "idle")
		if e := tunnel.Start(); e != nil {
			t.Fatal(e)
		}
		command := exec.Command(os.Args[0], "-test.run=^TestMacChildrenHelper$", "--", "stdin")
		// A race-instrumented Go child intentionally waits before exiting.
		// Allow its orderly exit rather than testing SDK forced termination.
		transport := &mcp.CommandTransport{Command: command, TerminateDuration: 3 * time.Second}
		connection, e := transport.Connect(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		leftover := exec.Command(os.Args[0], "-test.run=^TestMacChildrenHelper$", "--", "idle")
		if e = leftover.Start(); e != nil {
			t.Fatal(e)
		}
		fixture := childFixture{Parent: os.Getpid(), Guardian: ownedGuardianPID, Children: []int{tunnel.Process.Pid, command.Process.Pid, leftover.Process.Pid}}
		data, _ := json.Marshal(fixture)
		if e = os.WriteFile(args[1], data, 0600); e != nil {
			t.Fatal(e)
		}
		var input [1]byte
		if _, e = os.Stdin.Read(input[:]); e != nil {
			os.Exit(3)
		}
		cancel()
		_ = tunnel.Wait()
		if e = connection.Close(); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(args[1]+".closed", []byte("context-and-mcp-closed"), 0600); e != nil {
			t.Fatal(e)
		}
		// The leftover intentionally outlives normal caller cleanup; the
		// independent guardian must catch even a normally exiting parent.
		os.Exit(0)
	default:
		os.Exit(2)
	}
}

func TestMacGuardianRejectsUnrelatedPID(t *testing.T) {
	if handled, e := RunChildGuardian([]string{"version"}); handled || e != nil {
		t.Fatalf("ordinary command intercepted: %t %v", handled, e)
	}
	for _, args := range [][]string{{guardianCommand}, {guardianCommand, "not-a-pid"}, {guardianCommand, strconv.Itoa(os.Getpid())}, {guardianCommand, "1"}, {guardianCommand, "-10"}, {guardianCommand, "10", "extra"}} {
		if handled, e := RunChildGuardian(args); !handled || e == nil {
			t.Fatalf("unsafe guardian request accepted: %s", strings.Join(args, " "))
		}
	}
}
