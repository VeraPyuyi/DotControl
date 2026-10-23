package desktop

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// StartupStatus is a sanitized, cached local status; no executable, state path,
// registry data, or launchctl output is exposed to the console.
type StartupStatus struct {
	Configured bool   `json:"configured"`
	Mechanism  string `json:"mechanism"`
	Managed    bool   `json:"managed"`
	Reason     string `json:"reason"`
}

var startupStatusCache = struct {
	sync.RWMutex
	value StartupStatus
}{value: StartupStatus{Reason: "startup_not_checked"}}
var managedParentCheck = managedRuntimeParent

func managedRuntimeParent(executable string) bool {
	if runtime.GOOS == "darwin" {
		return os.Getppid() == 1
	}
	processes, e := listProgramProcesses(context.Background())
	if e != nil {
		return false
	}
	for _, p := range processes {
		if p.PID == os.Getppid() {
			return p.Executable != "" && strings.EqualFold(filepath.Clean(p.Executable), filepath.Clean(executable))
		}
	}
	return false
}

func CachedStartupStatus() StartupStatus {
	startupStatusCache.RLock()
	defer startupStatusCache.RUnlock()
	return startupStatusCache.value
}
func cacheStartupStatus(s StartupStatus) {
	startupStatusCache.Lock()
	startupStatusCache.value = s
	startupStatusCache.Unlock()
}

// RefreshStartupStatus is deliberately explicit: call once when the runtime
// starts, outside status polling and owner locks. Errors retain an unknown reason.
func RefreshStartupStatus(executable, state, managedMechanism string) {
	s, e := SnapshotStartup()
	result := StartupStatus{Reason: "startup_status_unavailable"}
	if e != nil {
		result.Reason = safeStartupError(e)
	}
	if e == nil {
		result.Configured = s.AutostartConfigured()
		switch {
		case s.TaskXML != "":
			result.Mechanism = "task-scheduler"
		case len(s.ManagedData) > 0 || len(s.Data) > 0 || s.Loaded:
			result.Mechanism = "launch-agent"
		case s.Value != "":
			result.Mechanism = "legacy-run"
		}
		result.Reason = "manual_start"
		if s.Present() && !s.BelongsToState(state) {
			result.Reason = "startup_other_instance"
		} else if managedMechanism != "" && managedMechanism == result.Mechanism && s.matchesExecutable(executable) && (s.TaskRunning || (!s.Disabled && s.launchRunning())) && managedParentCheck(executable) {
			result.Managed = true
			result.Reason = ""
		}
		if suspended, err := RuntimeSuspended(state); err != nil || suspended {
			result.Managed = false
			result.Reason = "startup_suspended"
		}
	}
	cacheStartupStatus(result)
}
func safeStartupError(e error) string {
	switch e.Error() {
	case "task_scheduler_access_denied", "task_scheduler_service_unavailable", "task_scheduler_definition_invalid", "task_scheduler_timeout", "task_scheduler_unavailable", "launch_agent_status_unavailable", "launch_agent_loaded_definition_unknown":
		return e.Error()
	}
	return "startup_status_unavailable"
}
func AutostartEnabled() bool { return CachedStartupStatus().Configured }
func (s StartupSnapshot) Present() bool {
	return s.Exists || s.TaskXML != "" || len(s.ManagedData) > 0 || s.Loaded
}

type taskDefinition struct {
	Actions struct {
		Exec []struct {
			Command   string `xml:"Command"`
			Arguments string `xml:"Arguments"`
		} `xml:"Exec"`
	} `xml:"Actions"`
	Triggers struct {
		Logon []struct {
			Enabled *bool `xml:"Enabled"`
		} `xml:"LogonTrigger"`
	} `xml:"Triggers"`
}

func parseTask(data string) (taskDefinition, error) {
	var t taskDefinition
	d := xml.NewDecoder(strings.NewReader(data))
	d.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	e := d.Decode(&t)
	if e == nil && len(t.Actions.Exec) != 1 {
		e = errors.New("startup_definition_invalid")
	}
	return t, e
}
func plistArguments(data []byte) ([]string, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	key := ""
	inArgs := false
	var args []string
	for {
		tok, e := d.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "key" {
				if e = d.DecodeElement(&key, &t); e != nil {
					return nil, e
				}
			} else if t.Name.Local == "array" && key == "ProgramArguments" {
				inArgs = true
			} else if t.Name.Local == "string" && inArgs {
				var value string
				if e = d.DecodeElement(&value, &t); e != nil {
					return nil, e
				}
				args = append(args, value)
			}
		case xml.EndElement:
			if t.Name.Local == "array" {
				inArgs = false
			}
		}
	}
	if len(args) < 4 {
		return nil, errors.New("startup_definition_invalid")
	}
	return args, nil
}
func plistBelongs(data []byte, state string) bool {
	a, e := plistArguments(data)
	return e == nil && len(a) >= 4 && a[1] == "--state" && a[2] == state && a[3] == "serve"
}
func (s StartupSnapshot) BelongsToState(state string) bool {
	if s.Value != "" && !strings.Contains(s.Value, " --state "+QuotedWindows(state)+" serve") {
		return false
	}
	if len(s.Data) > 0 && !plistBelongs(s.Data, state) {
		return false
	}
	if len(s.ManagedData) > 0 && !plistBelongs(s.ManagedData, state) {
		return false
	}
	if s.TaskXML != "" {
		t, e := parseTask(s.TaskXML)
		if e != nil {
			return false
		}
		prefix := "--state " + QuotedWindows(state)
		args := t.Actions.Exec[0].Arguments
		if !(args == prefix+" background-run" || args == prefix+" serve" || strings.HasPrefix(args, prefix+" serve ")) {
			return false
		}
	}
	// Loaded service without a restorable definition is not safe to replace.
	if s.Loaded && len(s.Data) == 0 && len(s.ManagedData) == 0 {
		return false
	}
	return !s.Exists || s.Value != "" || len(s.Data) > 0
}
func (s StartupSnapshot) matchesExecutable(exe string) bool {
	if s.TaskXML != "" {
		t, e := parseTask(s.TaskXML)
		return e == nil && strings.EqualFold(t.Actions.Exec[0].Command, exe)
	}
	data := s.ManagedData
	if len(data) == 0 {
		data = s.Data
	}
	a, e := plistArguments(data)
	return e == nil && a[0] == exe
}
func (s StartupSnapshot) AutostartConfigured() bool {
	if s.Value != "" {
		return true
	}
	if s.TaskXML != "" {
		t, e := parseTask(s.TaskXML)
		if e != nil || !s.TaskEnabled {
			return false
		}
		for _, l := range t.Triggers.Logon {
			if l.Enabled == nil || *l.Enabled {
				return true
			}
		}
		return false
	}
	return s.Exists && len(s.Data) > 0 && !s.Disabled
}
func xmlText(value string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(value))
	return b.String()
}
func windowsTaskXML(executable, state, sid string, login bool) string {
	enabled := "false"
	if login {
		enabled = "true"
	}
	return `<?xml version="1.0" encoding="UTF-16"?><Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task"><RegistrationInfo><Description>DotControl per-user desktop agent</Description></RegistrationInfo><Triggers><LogonTrigger><Enabled>` + enabled + `</Enabled><UserId>` + xmlText(sid) + `</UserId></LogonTrigger></Triggers><Principals><Principal id="User"><UserId>` + xmlText(sid) + `</UserId><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal></Principals><Settings><MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy><DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries><StopIfGoingOnBatteries>false</StopIfGoingOnBatteries><AllowHardTerminate>false</AllowHardTerminate><StartWhenAvailable>false</StartWhenAvailable><AllowStartOnDemand>true</AllowStartOnDemand><Enabled>true</Enabled><ExecutionTimeLimit>PT0S</ExecutionTimeLimit></Settings><Actions Context="User"><Exec><Command>` + xmlText(executable) + `</Command><Arguments>` + xmlText("--state "+QuotedWindows(state)+" background-run") + `</Arguments></Exec></Actions></Task>`
}
func launchAgentXML(label, exe, state string) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>Label</key><string>` + xmlText(label) + `</string><key>ProgramArguments</key><array><string>` + xmlText(exe) + `</string><string>--state</string><string>` + xmlText(state) + `</string><string>serve</string><string>--managed-start</string><string>launch-agent</string></array><key>RunAtLoad</key><true/><key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict><key>ProcessType</key><string>Interactive</string><key>LimitLoadToSessionType</key><string>Aqua</string><key>ThrottleInterval</key><integer>60</integer></dict></plist>`)
}

// The durable interlock closes the launchd disable/exit race without signalling
// a running process: an OS retry observes it before acquiring engine ownership
// and exits successfully. Only confirmed activation/restoration removes it.
func RuntimeSuspended(state string) (bool, error) {
	_, e := os.Lstat(filepath.Join(state, "background-suspended"))
	if os.IsNotExist(e) {
		return false, nil
	}
	return e == nil, e
}
func suspendRuntime(state string) error {
	return os.WriteFile(filepath.Join(state, "background-suspended"), []byte("installation\n"), 0600)
}
func ResumeStartup(state string) error {
	e := os.Remove(filepath.Join(state, "background-suspended"))
	if os.IsNotExist(e) {
		return nil
	}
	return e
}
