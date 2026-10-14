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
	// Loaded definitions survive plist edits/removal. Inspect every job in both
	// user domains before deleting an immutable program generation.
	for _, domain := range []string{launchDomain(), "user/" + strconv.Itoa(os.Getuid())} {
		b, e := launchCommand(ctx, "print", domain)
		if e != nil {
			return "", errors.New("startup_references_unavailable")
		}
		out.Write(b)
		for _, label := range loadedServiceLabels(string(b)) {
			data, e := launchCommand(ctx, "print", domain+"/"+label)
			if e != nil {
				return "", errors.New("startup_references_unavailable")
			}
			out.Write(data)
		}
	}
	return out.String(), nil
}

func loadedServiceLabels(data string) []string {
	var out []string
	inServices := false
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "services = {" {
			inServices = true
			continue
		}
		if inServices && line == "}" {
			break
		}
		if inServices {
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				label := fields[len(fields)-1]
				if !strings.ContainsAny(label, "/{}\"=") {
					out = append(out, label)
				}
			}
		}
	}
	return out
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
