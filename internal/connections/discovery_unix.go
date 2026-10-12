//go:build !windows

package connections

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
)

func listProcesses(ctx context.Context) ([]processInfo, error) {
	// ps comm contains the executable path, not process arguments or environment.
	result, err := runCommandWithStdoutLimit(ctx, "/bin/ps", []string{"-axo", "pid=,ppid=,comm="}, processMetadataLimit)
	if err != nil {
		return nil, err
	}
	return parseProcessMetadata(result)
}
func bundledPaths(processes []processInfo) []string {
	if runtime.GOOS != "darwin" {
		return nil
	}
	patterns := []string{"/Applications/Codex.app/Contents/Resources/codex"}
	if home, err := os.UserHomeDir(); err == nil {
		patterns = append(patterns, filepath.Join(home, "Applications", "Codex.app", "Contents", "Resources", "codex"), filepath.Join(home, "Library", "Application Support", "Codex", "bin", "*", "codex"))
	}
	for _, p := range processes {
		if desktopApp(p.Path) {
			patterns = append(patterns, filepath.Join(filepath.Dir(filepath.Dir(p.Path)), "Resources", "codex"))
		}
	}
	var paths []string
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(pattern)
		paths = append(paths, matches...)
	}
	return paths
}
