//go:build darwin

package desktop

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func SnapshotStartup() (StartupSnapshot, error) {
	p := plistPath()
	info, e := os.Lstat(p)
	if os.IsNotExist(e) {
		return StartupSnapshot{}, nil
	}
	if e != nil || !info.Mode().IsRegular() {
		return StartupSnapshot{}, errors.New("autostart_snapshot_failed")
	}
	b, e := os.ReadFile(p)
	if e != nil {
		return StartupSnapshot{}, errors.New("autostart_snapshot_failed")
	}
	return StartupSnapshot{Exists: true, Data: b, Mode: uint32(info.Mode().Perm())}, nil
}
func startupReferenceText(ctx context.Context) (string, error) {
	home, e := os.UserHomeDir()
	if e != nil {
		return "", e
	}
	var out strings.Builder
	for _, dir := range []string{filepath.Join(home, "Library", "LaunchAgents"), "/Library/LaunchAgents", "/Library/LaunchDaemons"} {
		entries, e := os.ReadDir(dir)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return "", errors.New("startup_references_unavailable")
		}
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".plist") {
				continue
			}
			p := filepath.Join(dir, entry.Name())
			info, e := os.Lstat(p)
			if e != nil || !info.Mode().IsRegular() || info.Size() > 2<<20 {
				return "", errors.New("startup_references_unavailable")
			}
			data, e := os.ReadFile(p)
			if e != nil {
				return "", errors.New("startup_references_unavailable")
			}
			if strings.HasPrefix(string(data), "bplist") {
				data, e = exec.CommandContext(ctx, "/usr/bin/plutil", "-convert", "xml1", "-o", "-", p).Output()
				if e != nil {
					return "", errors.New("startup_references_unavailable")
				}
			}
			out.Write(data)
			out.WriteByte('\n')
		}
	}
	return out.String(), nil
}
func RestoreStartup(s StartupSnapshot) error {
	p := plistPath()
	if !s.Exists {
		e := os.Remove(p)
		if os.IsNotExist(e) {
			return nil
		}
		return e
	}
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		return e
	}
	return os.WriteFile(p, s.Data, os.FileMode(s.Mode))
}
func listProgramProcesses(ctx context.Context) ([]programProcess, error) {
	b, e := exec.CommandContext(ctx, "/bin/ps", "-ww", "-axo", "pid=,ppid=,comm=").Output()
	if e != nil {
		return nil, errors.New("process_references_unavailable")
	}
	var out []programProcess
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) < 3 {
			return nil, errors.New("process_references_unavailable")
		}
		pid, e1 := strconv.Atoi(fields[0])
		parent, e2 := strconv.Atoi(fields[1])
		if e1 != nil || e2 != nil {
			return nil, errors.New("process_references_unavailable")
		}
		path := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), fields[0])), fields[1]))
		p := programProcess{PID: pid, ParentPID: parent, Name: filepath.Base(path)}
		if filepath.IsAbs(path) {
			p.Executable = path
		}
		out = append(out, p)
	}
	return out, nil
}
