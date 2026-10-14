//go:build darwin

package desktop

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const launchAgentLabel = "org.dotcontrol.agent"

var launchCommand = runLaunchCommand

func launchDomain() string  { return "gui/" + strconv.Itoa(os.Getuid()) }
func launchService() string { return launchDomain() + "/" + launchAgentLabel }
func managedPlistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Application Support", "DotControl", "background-agent.plist")
}
func runLaunchCommand(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "/bin/launchctl", args...).CombinedOutput()
}
func readStartupFile(p string) ([]byte, uint32, error) {
	i, e := os.Lstat(p)
	if os.IsNotExist(e) {
		return nil, 0, nil
	}
	if e != nil || !i.Mode().IsRegular() || i.Size() > 2<<20 {
		return nil, 0, errors.New("autostart_snapshot_failed")
	}
	b, e := os.ReadFile(p)
	return b, uint32(i.Mode().Perm()), e
}
func launchMissing(data []byte) bool {
	s := strings.ToLower(string(data))
	return strings.Contains(s, "could not find service") || strings.Contains(s, "service not found")
}
func launchField(data, key string) string {
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, key+" = ") {
			return strings.Trim(strings.TrimPrefix(line, key+" = "), `"`)
		}
	}
	return ""
}
func launchArguments(data string) []string {
	var args []string
	inside := false
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "arguments = {" {
			inside = true
			continue
		}
		if inside && line == "}" {
			break
		}
		if inside && line != "" {
			if at := strings.Index(line, " = "); at > 0 {
				if _, e := strconv.Atoi(line[:at]); e == nil {
					line = line[at+3:]
				}
			}
			args = append(args, strings.Trim(line, `"`))
		}
	}
	return args
}
func SnapshotStartup() (StartupSnapshot, error) {
	var s StartupSnapshot
	var e error
	s.Data, s.Mode, e = readStartupFile(plistPath())
	if e != nil {
		return s, e
	}
	s.Exists = len(s.Data) > 0
	s.ManagedData, s.ManagedMode, e = readStartupFile(managedPlistPath())
	if e != nil {
		return s, e
	}
	b, e := launchCommand(context.Background(), "print-disabled", launchDomain())
	if e != nil {
		return s, errors.New("launch_agent_status_unavailable")
	}
	s.Disabled = strings.Contains(string(b), `"`+launchAgentLabel+`" => true`)
	b, e = launchCommand(context.Background(), "print", launchService())
	if e != nil {
		if launchMissing(b) {
			return s, nil
		}
		return s, errors.New("launch_agent_status_unavailable")
	}
	s.Loaded = true
	s.LoadedText = string(b)
	// A loaded job may retain an old definition even after its plist was edited.
	// Do not replace it unless both the recorded source and executable agree.
	data := s.ManagedData
	source := managedPlistPath()
	if launchField(s.LoadedText, "path") == plistPath() {
		data = s.Data
		source = plistPath()
	}
	args, err := plistArguments(data)
	if err != nil || launchField(s.LoadedText, "path") != source || launchField(s.LoadedText, "program") != args[0] {
		return s, errors.New("launch_agent_loaded_definition_unknown")
	}
	actual := launchArguments(s.LoadedText)
	if len(actual) != len(args) {
		return s, errors.New("launch_agent_loaded_definition_unknown")
	}
	for i := range args {
		if actual[i] != args[i] {
			return s, errors.New("launch_agent_loaded_definition_unknown")
		}
	}
	return s, nil
}
func writeStartupFile(p string, data []byte, mode uint32) error {
	if len(data) == 0 {
		e := os.Remove(p)
		if os.IsNotExist(e) {
			return nil
		}
		return e
	}
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		return e
	}
	if mode == 0 {
		mode = 0600
	}
	f, e := os.CreateTemp(filepath.Dir(p), ".background-")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(os.FileMode(mode)); e == nil {
		_, e = f.Write(data)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp, p)
}
func launchChange(args ...string) error {
	_, e := launchCommand(context.Background(), args...)
	if e != nil {
		return errors.New("launch_agent_unavailable")
	}
	return nil
}

// bootout is only safe after graceful exit was independently confirmed. Never
// use kickstart -k, kill, or bootout against a running job.
func unloadStoppedAgent() error {
	b, e := launchCommand(context.Background(), "print", launchService())
	if e != nil {
		if launchMissing(b) {
			return nil
		}
		return errors.New("launch_agent_status_unavailable")
	}
	if launchField(string(b), "pid") != "" {
		return errors.New("startup_runtime_still_running")
	}
	return launchChange("bootout", launchService())
}
func SuspendStartup(state string) error {
	s, e := SnapshotStartup()
	if e != nil {
		return e
	}
	if !s.BelongsToState(state) {
		return errors.New("startup_instance_selection_required")
	}
	if e = suspendRuntime(state); e != nil {
		return e
	}
	return launchChange("disable", launchService())
}
func ConfigureStartup(exe, state string, login bool) error {
	s, e := SnapshotStartup()
	if e != nil {
		return e
	}
	if !s.BelongsToState(state) {
		return errors.New("startup_instance_selection_required")
	}
	if e = unloadStoppedAgent(); e != nil {
		return e
	}
	data := launchAgentXML(launchAgentLabel, exe, state)
	if e = writeStartupFile(managedPlistPath(), data, 0600); e != nil {
		return e
	}
	var loginData []byte
	if login {
		loginData = data
	}
	if e = writeStartupFile(plistPath(), loginData, 0600); e != nil {
		return e
	}
	// A backing plist outside LaunchAgents gives explicit on-demand starts OS
	// supervision while an absent login plist preserves autostart=false.
	return launchChange("enable", launchService())
}
func StartManaged(exe, state string) error {
	s, e := SnapshotStartup()
	if e != nil {
		return e
	}
	if !s.BelongsToState(state) || !s.matchesExecutable(exe) {
		return errors.New("managed_startup_unavailable")
	}
	if s.Loaded {
		if launchField(s.LoadedText, "pid") != "" {
			return nil
		}
		return launchChange("kickstart", launchService())
	}
	if s.Disabled {
		return errors.New("managed_startup_disabled")
	}
	p := managedPlistPath()
	if len(s.ManagedData) == 0 {
		p = plistPath()
	}
	return launchChange("bootstrap", launchDomain(), p)
}

// Remove the stopped candidate while the durable interlock still exists, so
// no pending launchd retry can observe the later interlock release.
func PrepareStartupRestore(state string) error {
	s, e := SnapshotStartup()
	if e != nil {
		return e
	}
	if !s.BelongsToState(state) {
		return errors.New("startup_instance_selection_required")
	}
	return unloadStoppedAgent()
}
func RestoreStartup(s StartupSnapshot) error {
	// A drain refusal leaves the original process running. Restore only the
	// override when the exact original loaded definition is still present;
	// unloading would send a termination signal to that process.
	current, e := SnapshotStartup()
	if e != nil {
		return e
	}
	if current.Loaded && launchField(current.LoadedText, "pid") != "" {
		if !s.Loaded || !bytes.Equal(current.Data, s.Data) || !bytes.Equal(current.ManagedData, s.ManagedData) || current.Mode != s.Mode || current.ManagedMode != s.ManagedMode || launchField(current.LoadedText, "path") != launchField(s.LoadedText, "path") {
			return errors.New("startup_runtime_still_running")
		}
		verb := "enable"
		if s.Disabled {
			verb = "disable"
		}
		return launchChange(verb, launchService())
	}
	if e := unloadStoppedAgent(); e != nil {
		return e
	}
	if e := writeStartupFile(plistPath(), s.Data, s.Mode); e != nil {
		return e
	}
	if e := writeStartupFile(managedPlistPath(), s.ManagedData, s.ManagedMode); e != nil {
		return e
	}
	if s.Loaded {
		if e := launchChange("enable", launchService()); e != nil {
			return e
		}
		p := managedPlistPath()
		if len(s.ManagedData) == 0 {
			p = plistPath()
		}
		if e := launchChange("bootstrap", launchDomain(), p); e != nil {
			return e
		}
	}
	verb := "enable"
	if s.Disabled {
		verb = "disable"
	}
	return launchChange(verb, launchService())
}
func RemoveStartup(state string) error {
	s, e := SnapshotStartup()
	if e != nil {
		return e
	}
	if !s.BelongsToState(state) {
		return errors.New("startup_instance_selection_required")
	}
	if e = unloadStoppedAgent(); e != nil {
		return e
	}
	if e = writeStartupFile(plistPath(), nil, 0); e != nil {
		return e
	}
	return writeStartupFile(managedPlistPath(), nil, 0)
}
func Autostart(exe, state string, enable bool) error {
	if !enable {
		return RemoveStartup(state)
	}
	return ConfigureStartup(exe, state, true)
}
