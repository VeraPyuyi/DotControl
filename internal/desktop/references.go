package desktop

import (
	"context"
	"errors"
	"html"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// StartupSnapshot retains the existing per-user registration exactly. Its
// contents stay in the private installation journal, never progress output.
type StartupSnapshot struct {
	Exists      bool   `json:"exists"`
	Value       string `json:"value,omitempty"`
	Kind        uint32 `json:"kind,omitempty"`
	Data        []byte `json:"data,omitempty"`
	Mode        uint32 `json:"mode,omitempty"`
	TaskXML     string `json:"task_xml,omitempty"`
	TaskEnabled bool   `json:"task_enabled,omitempty"`
	TaskRunning bool   `json:"task_running,omitempty"`
	ManagedData []byte `json:"managed_data,omitempty"`
	ManagedMode uint32 `json:"managed_mode,omitempty"`
	Loaded      bool   `json:"loaded,omitempty"`
	Disabled    bool   `json:"disabled,omitempty"`
	LoadedText  string `json:"loaded_text,omitempty"`
}
type programProcess struct {
	PID, ParentPID   int
	Name, Executable string
}

func pathInside(root, p string) bool {
	root = filepath.Clean(root)
	p = filepath.Clean(p)
	// Case folding is conservative on case-sensitive macOS volumes as well.
	return strings.EqualFold(root, p) || strings.HasPrefix(strings.ToLower(p), strings.ToLower(root)+string(filepath.Separator))
}
func checkProgramReferences(root string, processes []programProcess, startup string) error {
	for _, p := range processes {
		if p.Executable != "" && pathInside(root, p.Executable) {
			return errors.New("program_process_referenced")
		}
		if p.Executable == "" {
			switch strings.ToLower(p.Name) {
			case "dotcontrol", "dotcontrol.exe", "tunnel-client", "tunnel-client.exe", "cua-driver", "cua-driver.exe", "dotcontrolactivity", "dotcontrolmenubar":
				return errors.New("program_process_reference_unknown")
			}
		}
	}
	startup = html.UnescapeString(startup)
	if filepath.Separator == '\\' {
		startup = strings.ReplaceAll(startup, "/", `\`)
	}
	if strings.Contains(strings.ToLower(startup), strings.ToLower(filepath.Clean(root))) {
		return errors.New("program_startup_referenced")
	}
	return nil
}

// CheckProgramReferences checks the whole immutable generation, including all
// transport/helper executables. It never signals processes or removes startup.
func CheckProgramReferences(ctx context.Context, root string) error {
	processes, err := listProgramProcesses(ctx)
	if err != nil {
		return err
	}
	startup, err := startupReferenceText(ctx)
	if err != nil {
		return err
	}
	return checkProgramReferences(root, processes, startup)
}

func ExecutableRunning(ctx context.Context, exe string) (bool, error) {
	processes, e := listProgramProcesses(ctx)
	if e != nil {
		return false, e
	}
	for _, p := range processes {
		if p.Executable != "" && strings.EqualFold(filepath.Clean(p.Executable), filepath.Clean(exe)) {
			return true, nil
		}
		if p.Executable == "" && strings.EqualFold(p.Name, filepath.Base(exe)) {
			return false, errors.New("program_process_reference_unknown")
		}
	}
	return false, nil
}

// WaitExecutableExit catches the interval after the engine lock is released
// but before the agent or its bundled transports have left the process table.
func WaitExecutableExit(ctx context.Context, paths []string) error {
	wait, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		active := false
		processes, e := listProgramProcesses(wait)
		if e != nil {
			return e
		}
		for _, p := range paths {
			if p == "" || !filepath.IsAbs(p) {
				continue
			}
			for _, process := range processes {
				// An installer invoked from an old generation must be allowed to
				// activate; the cleanup guard keeps its own executable until exit.
				if process.PID == os.Getpid() {
					continue
				}
				if process.Executable != "" && strings.EqualFold(filepath.Clean(process.Executable), filepath.Clean(p)) {
					active = true
				}
				if process.Executable == "" && strings.EqualFold(process.Name, filepath.Base(p)) {
					return errors.New("program_process_reference_unknown")
				}
			}
		}
		if !active {
			return nil
		}
		select {
		case <-wait.Done():
			return errors.New("agent_process_exit_timeout")
		case <-ticker.C:
		}
	}
}

// ProcessTree remembers the agent and every observed descendant. Keeping the
// PIDs until absent is conservative under PID reuse and cannot stop another app.
type ProcessTree struct {
	mu  sync.Mutex
	ids map[int]bool
}

func ObserveProcessTree(ctx context.Context, pid int) (*ProcessTree, error) {
	if pid <= 0 {
		return nil, errors.New("agent_process_identity_unverified")
	}
	tree := &ProcessTree{ids: map[int]bool{pid: true}}
	if _, err := tree.remaining(ctx); err != nil {
		return nil, err
	}
	return tree, nil
}
func (t *ProcessTree) remaining(ctx context.Context) (bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	processes, err := listProgramProcesses(ctx)
	if err != nil {
		return true, err
	}
	for changed := true; changed; {
		changed = false
		for _, p := range processes {
			if t.ids[p.ParentPID] && !t.ids[p.PID] {
				t.ids[p.PID] = true
				changed = true
			}
		}
	}
	for _, p := range processes {
		if t.ids[p.PID] {
			return true, nil
		}
	}
	return false, nil
}

// Watch records descendants throughout draining, including children created by
// a previously accepted task while the shutdown HTTP call is still pending.
func (t *ProcessTree) PIDs() []int {
	t.mu.Lock()
	defer t.mu.Unlock()
	ids := make([]int, 0, len(t.ids))
	for id := range t.ids {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

func (t *ProcessTree) Watch(ctx context.Context, record func([]int) error) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	count := len(t.PIDs())
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if _, e := t.remaining(ctx); e != nil {
				if ctx.Err() != nil {
					return nil
				}
				return e
			}
			ids := t.PIDs()
			if len(ids) != count && record != nil {
				if e := record(ids); e != nil {
					return e
				}
				count = len(ids)
			}
		}
	}
}

// Persisted PID evidence is conservative under reuse: an occupied recorded PID
// blocks recovery instead of authorizing a duplicate runtime or cleanup.
func WaitProcessIDs(ctx context.Context, ids []int, record func([]int) error) error {
	if len(ids) == 0 {
		return nil
	}
	tree := &ProcessTree{ids: map[int]bool{}}
	for _, id := range ids {
		if id <= 0 {
			return errors.New("process_evidence_invalid")
		}
		tree.ids[id] = true
	}
	wait, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return tree.wait(wait, record)
}
func (t *ProcessTree) Wait(ctx context.Context) error {
	return t.wait(ctx, nil)
}
func (t *ProcessTree) wait(ctx context.Context, record func([]int) error) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	count := len(t.PIDs())
	for {
		active, err := t.remaining(ctx)
		if err != nil {
			return err
		}
		ids := t.PIDs()
		if record != nil && len(ids) != count {
			if e := record(ids); e != nil {
				return e
			}
			count = len(ids)
		}
		if !active {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("agent_process_exit_timeout")
		case <-ticker.C:
		}
	}
}
