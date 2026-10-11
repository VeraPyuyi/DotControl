//go:build darwin

package desktop

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const guardianCommand = "__dotcontrol-child-guardian"

var childOwnershipOnce sync.Once
var childOwnershipErr error
var ownedGuardianPID int

// OwnChildren starts an independent, kernel-notified guardian before serve
// launches any runtime subprocesses. Unlike CommandContext cancellation this
// survives an agent SIGKILL. Call it before starting any children.
//
// The agent and guardian share a new private process group; ordinary children
// inherit it. LaunchServices applications, existing Cua daemons and explicitly
// detached user applications are outside it and are never signalled here.
func OwnChildren() error {
	childOwnershipOnce.Do(func() { childOwnershipErr = startChildGuardian() })
	return childOwnershipErr
}

func startChildGuardian() error {
	pid := os.Getpid()
	if unix.Getpgrp() != pid {
		if e := unix.Setpgid(0, 0); e != nil {
			return errors.New("process_supervision_unavailable")
		}
	}
	if unix.Getpgrp() != pid {
		return errors.New("process_supervision_unavailable")
	}
	executable, e := os.Executable()
	if e != nil {
		return errors.New("process_supervision_unavailable")
	}
	reader, writer, e := os.Pipe()
	if e != nil {
		return errors.New("process_supervision_unavailable")
	}
	defer reader.Close()
	command := exec.Command(executable, guardianCommand, strconv.Itoa(pid))
	command.ExtraFiles = []*os.File{writer} // readiness only, never credentials
	if e = command.Start(); e != nil {
		writer.Close()
		return errors.New("process_supervision_unavailable")
	}
	writer.Close()
	_ = reader.SetReadDeadline(time.Now().Add(5 * time.Second))
	var ready [1]byte
	if _, e = io.ReadFull(reader, ready[:]); e != nil || ready[0] != 1 {
		_ = command.Process.Kill()
		_ = command.Wait()
		return errors.New("process_supervision_unavailable")
	}
	ownedGuardianPID = command.Process.Pid
	// Losing the guardian while the agent is alive invalidates supervision.
	// The agent itself pins its private PGID, so this cannot hit a reused group.
	go func() { _ = command.Wait(); _ = unix.Kill(-pid, unix.SIGKILL) }()
	return nil
}

// RunChildGuardian must be dispatched by main before flags/configuration and
// before any native capability is initialized. Re-executing the same packaged
// binary avoids a separately installed helper. It uses public kqueue NOTE_EXIT.
// A true result means the internal command was handled; main must then exit.
func RunChildGuardian(args []string) (bool, error) {
	if len(args) == 0 || args[0] != guardianCommand {
		return false, nil
	}
	if len(args) != 2 {
		return true, errors.New("invalid_child_guardian")
	}
	parent, e := strconv.Atoi(args[1])
	if e != nil || parent <= 1 || parent == os.Getpid() || os.Getppid() != parent || unix.Getpgrp() != parent {
		return true, errors.New("invalid_child_guardian")
	}
	group, e := unix.Getpgid(parent)
	if e != nil || group != parent {
		return true, errors.New("invalid_child_guardian")
	}
	// The agent handles graceful stop. A group-level interrupt must not kill
	// its guardian first and turn orderly shutdown into an immediate SIGKILL.
	signal.Ignore(os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	ready := os.NewFile(3, "guardian-ready")
	if ready == nil {
		return true, errors.New("invalid_child_guardian_pipe")
	}
	defer ready.Close()
	info, e := ready.Stat()
	if e != nil || info.Mode()&os.ModeNamedPipe == 0 {
		return true, errors.New("invalid_child_guardian_pipe")
	}
	kq, e := unix.Kqueue()
	if e != nil {
		return true, errors.New("child_guardian_watch_failed")
	}
	defer unix.Close(kq)
	unix.CloseOnExec(kq)
	change := unix.Kevent_t{Ident: uint64(parent), Filter: unix.EVFILT_PROC, Flags: unix.EV_ADD | unix.EV_ENABLE | unix.EV_ONESHOT, Fflags: unix.NOTE_EXIT}
	if _, e = unix.Kevent(kq, []unix.Kevent_t{change}, nil, nil); e != nil {
		// No runtime child can have started before readiness. If the parent
		// already disappeared, its private group is still pinned by us.
		if os.Getppid() != parent {
			_ = unix.Kill(-parent, unix.SIGKILL)
		}
		return true, errors.New("child_guardian_watch_failed")
	}
	if os.Getppid() != parent {
		return true, unix.Kill(-parent, unix.SIGKILL)
	}
	if _, e = ready.Write([]byte{1}); e != nil {
		return true, unix.Kill(-parent, unix.SIGKILL)
	}
	ready.Close()
	events := make([]unix.Kevent_t, 1)
	for {
		n, e := unix.Kevent(kq, nil, events, nil)
		if e == unix.EINTR {
			continue
		}
		if e != nil {
			return true, unix.Kill(-parent, unix.SIGKILL)
		}
		if n > 0 && (events[0].Fflags&unix.NOTE_EXIT != 0 || events[0].Flags&unix.EV_ERROR != 0) {
			// We intentionally remain in the group until this one group kill.
			// Our membership reserves its PGID even if parent/children exited,
			// preventing a late notification from signalling an unrelated group.
			return true, unix.Kill(-parent, unix.SIGKILL)
		}
	}
}
