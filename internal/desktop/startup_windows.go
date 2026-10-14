//go:build windows

package desktop

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"time"
	"unicode/utf16"
)

type taskRequest struct {
	Operation string
	Name      string
	XML       string
	Enabled   bool
}
type taskResult struct {
	XML        string
	SID        string
	Enabled    bool
	Running    bool
	References string
	Error      string
	LastResult int64
	State      int
}

var taskOperation = runTaskOperation
var readRunStartup = snapshotLegacyRun
var writeRunStartup = restoreLegacyRun

// The COM Task Scheduler API is present on supported Windows versions without
// modules, elevation, passwords, or a service account. Inputs travel as JSON on
// stdin and never become PowerShell source. All failures have sanitized output.
const taskScript = `$ErrorActionPreference='Stop'
[Console]::InputEncoding=New-Object System.Text.UTF8Encoding($false)
[Console]::OutputEncoding=New-Object System.Text.UTF8Encoding($false)
try {
$p=[Console]::In.ReadToEnd() | ConvertFrom-Json
$sid=[System.Security.Principal.WindowsIdentity]::GetCurrent().User.Value
$s=New-Object -ComObject Schedule.Service
$s.Connect()
$f=$s.GetFolder('\')
$name='DotControl-'+$sid
if ($p.Name) { $name=$p.Name }
function Existing { foreach ($t in $f.GetTasks(1)) { if ($t.Name -eq $name) { return $t } }; return $null }
switch ($p.Operation) {
 'snapshot' { $t=Existing; if ($null -eq $t) { @{SID=$sid} | ConvertTo-Json -Compress } else { @{SID=$sid;XML=$t.Xml;Enabled=[bool]$t.Enabled;Running=($t.State -eq 4);State=[int]$t.State;LastResult=[long]$t.LastTaskResult} | ConvertTo-Json -Compress } }
 'register' { $null=$f.RegisterTask($name,$p.XML,6,$sid,$null,3,$null); $t=Existing; $t.Enabled=[bool]$p.Enabled; '{}' }
 'disable' { $t=Existing; if ($null -ne $t) { $t.Enabled=$false }; '{}' }
 'enable' { $t=Existing; if ($null -eq $t) { throw 'missing' }; $t.Enabled=$true; '{}' }
 'start' { $t=Existing; if ($null -eq $t -or -not $t.Enabled) { throw 'disabled' }; if ($t.State -ne 4) { $null=$t.Run($null) }; '{}' }
 'remove' { if ($null -ne (Existing)) { $f.DeleteTask($name,0) }; '{}' }
 'references' { function Collect($folder) { foreach ($t in $folder.GetTasks(1)) { $t.Xml }; foreach ($c in $folder.GetFolders(0)) { Collect $c } }; @{References=((Collect $f) -join "` + "`n" + `")} | ConvertTo-Json -Compress -Depth 3 }
 default { throw 'invalid operation' }
}
} catch {
 $code='task_scheduler_unavailable'
 $cause=$_.Exception
 while ($null -ne $cause) {
  $hr='{0:X8}' -f ($cause.HResult -band 0xffffffffL)
  if ($hr -eq '80070005') { $code='task_scheduler_access_denied'; break }
  if ($hr -in @('80041315','800706BA')) { $code='task_scheduler_service_unavailable'; break }
  if ($hr -in @('80041316','80041317','80041318','80041319','8004131A')) { $code='task_scheduler_definition_invalid'; break }
  $cause=$cause.InnerException
 }
 @{Error=$code} | ConvertTo-Json -Compress
 exit 1
}`

func runTaskOperation(ctx context.Context, r taskRequest) (taskResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	units := utf16.Encode([]rune(taskScript))
	data := make([]byte, len(units)*2)
	for i, v := range units {
		binary.LittleEndian.PutUint16(data[i*2:], v)
	}
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(data))
	HideConsole(cmd)
	payload, _ := json.Marshal(r)
	cmd.Stdin = strings.NewReader(string(payload))
	b, e := cmd.Output()
	var result taskResult
	if ctx.Err() != nil {
		return taskResult{}, errors.New("task_scheduler_timeout")
	}
	parseErr := json.Unmarshal(bytesWithoutBOM(b), &result)
	if result.Error != "" {
		return taskResult{}, errors.New(safeStartupError(errors.New(result.Error)))
	}
	if e != nil || parseErr != nil {
		return taskResult{}, errors.New("task_scheduler_unavailable")
	}
	return result, nil
}
func bytesWithoutBOM(b []byte) []byte {
	return []byte(strings.TrimPrefix(strings.TrimSpace(string(b)), "\ufeff"))
}
func SnapshotStartup() (StartupSnapshot, error) {
	s, e := readRunStartup()
	if e != nil {
		return s, e
	}
	t, e := taskOperation(context.Background(), taskRequest{Operation: "snapshot"})
	if e != nil {
		return StartupSnapshot{}, e
	}
	s.TaskXML = t.XML
	s.TaskEnabled = t.Enabled
	s.TaskRunning = t.Running
	return s, nil
}
func RestoreStartup(s StartupSnapshot) error {
	var e error
	if s.TaskXML != "" {
		_, e = taskOperation(context.Background(), taskRequest{Operation: "register", XML: s.TaskXML, Enabled: s.TaskEnabled})
	} else {
		_, e = taskOperation(context.Background(), taskRequest{Operation: "remove"})
	}
	if e != nil {
		return e
	}
	return writeRunStartup(s)
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
	if s.TaskXML != "" {
		if _, e = taskOperation(context.Background(), taskRequest{Operation: "disable"}); e != nil {
			return e
		}
	}
	// Disabling a task never terminates its current action. Remove the old login
	// value as well, so a second login cannot race an in-progress upgrade.
	return writeRunStartup(StartupSnapshot{})
}
func ConfigureStartup(exe, state string, login bool) error {
	s, e := SnapshotStartup()
	if e != nil {
		return e
	}
	if !s.BelongsToState(state) {
		return errors.New("startup_instance_selection_required")
	}
	t, e := taskOperation(context.Background(), taskRequest{Operation: "snapshot"})
	if e != nil {
		return e
	}
	if _, e = taskOperation(context.Background(), taskRequest{Operation: "register", XML: windowsTaskXML(exe, state, t.SID, login), Enabled: true}); e != nil {
		return e
	}
	if e = writeRunStartup(StartupSnapshot{}); e != nil {
		return e
	}
	return nil
}
func StartManaged(exe, state string) error {
	s, e := SnapshotStartup()
	if e != nil {
		return e
	}
	if !s.BelongsToState(state) || !s.matchesExecutable(exe) || s.TaskXML == "" || !s.TaskEnabled {
		return errors.New("managed_startup_unavailable")
	}
	_, e = taskOperation(context.Background(), taskRequest{Operation: "start"})
	return e
}
func PrepareStartupRestore(string) error { return nil }
func RemoveStartup(state string) error {
	s, e := SnapshotStartup()
	if e != nil {
		return e
	}
	if !s.BelongsToState(state) {
		return errors.New("startup_instance_selection_required")
	}
	if _, e = taskOperation(context.Background(), taskRequest{Operation: "remove"}); e != nil {
		return e
	}
	return writeRunStartup(StartupSnapshot{})
}

// Kept for existing callers; false means remove this instance's registration.
func Autostart(exe, state string, enable bool) error {
	if !enable {
		return RemoveStartup(state)
	}
	return ConfigureStartup(exe, state, true)
}
