//go:build windows

package connections

import (
	"context"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"unsafe"
)

func listProcesses(ctx context.Context) ([]processInfo, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	var result []processInfo
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		p := processInfo{PID: entry.ProcessID, ParentPID: entry.ParentProcessID}
		h, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, entry.ProcessID)
		if e == nil {
			buffer := make([]uint16, 32768)
			size := uint32(len(buffer))
			if windows.QueryFullProcessImageName(h, 0, &buffer[0], &size) == nil {
				p.Path = windows.UTF16ToString(buffer[:size])
			}
			windows.CloseHandle(h)
		}
		result = append(result, p)
	}
	return result, nil
}
func bundledPaths(processes []processInfo) []string {
	var patterns []string
	for _, p := range processes {
		if desktopApp(p.Path) {
			app := filepath.Dir(p.Path)
			patterns = append(patterns, filepath.Join(app, "resources", "codex.exe"), filepath.Join(app, "resources", "bin", "codex.exe"))
		}
	}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		patterns = append(patterns, filepath.Join(local, "OpenAI", "Codex", "bin", "*", "codex.exe"))
	}
	if programFiles := os.Getenv("ProgramFiles"); programFiles != "" {
		patterns = append(patterns, filepath.Join(programFiles, "WindowsApps", "OpenAI.Codex_*", "app", "resources", "codex.exe"))
	}
	var paths []string
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(pattern)
		paths = append(paths, matches...)
	}
	return paths
}
