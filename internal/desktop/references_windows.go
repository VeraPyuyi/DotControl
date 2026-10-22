//go:build windows

package desktop

import (
	"context"
	"errors"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"strings"
	"unsafe"
)

const startupRegistryPath = `Software\Microsoft\Windows\CurrentVersion\Run`

// Check every Run/RunOnce value rather than only our own registration: another
// launcher can still reference an otherwise obsolete DotControl generation.
func startupReferenceText(ctx context.Context) (string, error) {
	var out strings.Builder
	for _, base := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		for _, path := range []string{startupRegistryPath, startupRegistryPath + "Once"} {
			if e := ctx.Err(); e != nil {
				return "", e
			}
			k, e := registry.OpenKey(base, path, registry.QUERY_VALUE)
			if e == registry.ErrNotExist {
				continue
			}
			if e != nil {
				return "", errors.New("startup_references_unavailable")
			}
			names, e := k.ReadValueNames(-1)
			if e != nil {
				k.Close()
				return "", errors.New("startup_references_unavailable")
			}
			for _, name := range names {
				value, kind, e := k.GetStringValue(name)
				if e != nil {
					k.Close()
					return "", errors.New("startup_references_unavailable")
				}
				if kind == registry.EXPAND_SZ {
					value, e = registry.ExpandString(value)
					if e != nil {
						k.Close()
						return "", errors.New("startup_references_unavailable")
					}
				}
				out.WriteString(value)
				out.WriteByte('\n')
			}
			k.Close()
		}
	}
	tasks, e := taskOperation(ctx, taskRequest{Operation: "references"})
	if e != nil {
		return "", errors.New("startup_references_unavailable")
	}
	out.WriteString(tasks.References)
	return out.String(), nil
}

func snapshotLegacyRun() (StartupSnapshot, error) {
	k, e := registry.OpenKey(registry.CURRENT_USER, startupRegistryPath, registry.QUERY_VALUE)
	if e == registry.ErrNotExist {
		return StartupSnapshot{}, nil
	}
	if e != nil {
		return StartupSnapshot{}, errors.New("autostart_snapshot_failed")
	}
	defer k.Close()
	value, kind, e := k.GetStringValue("DotControl")
	if e == registry.ErrNotExist {
		return StartupSnapshot{}, nil
	}
	if e != nil {
		return StartupSnapshot{}, errors.New("autostart_snapshot_failed")
	}
	return StartupSnapshot{Exists: true, Value: value, Kind: kind}, nil
}
func restoreLegacyRun(s StartupSnapshot) error {
	k, _, e := registry.CreateKey(registry.CURRENT_USER, startupRegistryPath, registry.SET_VALUE)
	if e != nil {
		return errors.New("autostart_restore_failed")
	}
	defer k.Close()
	if !s.Exists {
		e = k.DeleteValue("DotControl")
		if e == registry.ErrNotExist {
			return nil
		}
		return e
	}
	if s.Kind == registry.SZ {
		return k.SetStringValue("DotControl", s.Value)
	}
	if s.Kind == registry.EXPAND_SZ {
		return k.SetExpandStringValue("DotControl", s.Value)
	}
	return errors.New("autostart_snapshot_invalid")
}
func listProgramProcesses(ctx context.Context) ([]programProcess, error) {
	snap, e := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if e != nil {
		return nil, errors.New("process_references_unavailable")
	}
	defer windows.CloseHandle(snap)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	e = windows.Process32First(snap, &entry)
	var out []programProcess
	for e == nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		p := programProcess{PID: int(entry.ProcessID), ParentPID: int(entry.ParentProcessID), Name: windows.UTF16ToString(entry.ExeFile[:])}
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, entry.ProcessID)
		if err == nil {
			var created, exited, kernel, user windows.Filetime
			if windows.GetProcessTimes(h, &created, &exited, &kernel, &user) == nil {
				p.Created = uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime)
			}
			buf := make([]uint16, 32768)
			n := uint32(len(buf))
			if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) == nil {
				p.Executable = windows.UTF16ToString(buf[:n])
			}
			windows.CloseHandle(h)
		}
		out = append(out, p)
		e = windows.Process32Next(snap, &entry)
	}
	if e != windows.ERROR_NO_MORE_FILES {
		return nil, errors.New("process_references_unavailable")
	}
	return out, nil
}
