package connections

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoveryPrefersRunningDesktopOverPATH(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "legacy", "codex")
	bundled := filepath.Join(dir, "bundle", "codex")
	running := filepath.Join(dir, "running", "codex")
	var probes []string
	got, err := DiscoverCodex(context.Background(), "", DiscoveryOptions{
		Candidates: func(context.Context) ([]Candidate, error) {
			return []Candidate{{Path: legacy, Source: SourcePATH}, {Path: bundled, Source: SourceBundled}, {Path: running, Source: SourceRunningDesktop}}, nil
		},
		Version: func(ctx context.Context, path string) (string, error) {
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("unbounded version probe")
			}
			probes = append(probes, path)
			return "codex-cli 0.120.0-alpha.9\n", nil
		},
	})
	if err != nil || got.Executable != running || got.Source != SourceRunningDesktop || got.Version != "0.120.0-alpha.9" || len(probes) != 1 {
		t.Fatalf("wrong candidate: %#v %v probes=%v", got, err, probes)
	}
	b, _ := json.Marshal(got)
	if strings.Contains(string(b), dir) {
		t.Fatal("discovery diagnostics leak filesystem")
	}
}

func TestDiscoveryFallsBackWhenDesktopVersionFails(t *testing.T) {
	dir := t.TempDir()
	desktop := filepath.Join(dir, "app", "codex")
	legacy := filepath.Join(dir, "path", "codex")
	got, err := DiscoverCodex(context.Background(), "", DiscoveryOptions{
		Candidates: func(context.Context) ([]Candidate, error) {
			return []Candidate{{Path: desktop, Source: SourceBundled}, {Path: legacy, Source: SourcePATH}}, nil
		},
		Version: func(_ context.Context, path string) (string, error) {
			if path == desktop {
				return "private details", errors.New("private path")
			}
			return "codex-cli 1.0.0", nil
		},
	})
	if err != nil || got.Executable != legacy || got.Source != SourcePATH {
		t.Fatalf("%#v %v", got, err)
	}
}

func TestDiscoveryExplicitOverrideFailsClosed(t *testing.T) {
	explicit := filepath.Join(t.TempDir(), "my-codex")
	called := false
	got, err := DiscoverCodex(context.Background(), explicit, DiscoveryOptions{
		Candidates: func(context.Context) ([]Candidate, error) {
			t.Fatal("override should not discover fallback")
			return nil, nil
		},
		Version: func(_ context.Context, path string) (string, error) {
			called = true
			if path != explicit {
				t.Fatal(path)
			}
			return "not codex; private/path", nil
		},
	})
	if !called || err == nil || got.Code != "codex_version_unavailable" || strings.Contains(err.Error(), explicit) {
		t.Fatalf("%#v %v", got, err)
	}
}

func TestDiscoveryFiltersUntrustedSourcesAndRedactsProbeErrors(t *testing.T) {
	called := false
	got, err := DiscoverCodex(context.Background(), "", DiscoveryOptions{
		Candidates: func(context.Context) ([]Candidate, error) {
			return []Candidate{{Path: "relative/codex", Source: SourcePATH}, {Path: filepath.Join(t.TempDir(), "codex"), Source: "private secret"}}, nil
		},
		Version: func(context.Context, string) (string, error) { called = true; return "", nil },
	})
	if called || err == nil || got.Code != "codex_not_found" {
		t.Fatalf("%#v %v", got, err)
	}
}

func TestDesktopAncestryRejectsUnrelatedProcesses(t *testing.T) {
	processes := []processInfo{
		{PID: 1, Path: "C:/Program Files/WindowsApps/OpenAI.Codex_1_x64__example/app/ChatGPT.exe"},
		{PID: 2, ParentPID: 1, Path: "C:/Users/example/AppData/Local/OpenAI/Codex/bin/version/codex.exe"},
		{PID: 3, Path: "C:/Users/example/AppData/Local/Programs/OpenAI/Codex/bin/codex.exe"},
		{PID: 4, ParentPID: 1, Path: "C:/unrelated/codex.exe"},
	}
	candidates := runningDesktopCandidates(processes)
	if len(candidates) != 1 || candidates[0].Path != processes[1].Path || candidates[0].Source != SourceRunningDesktop {
		t.Fatalf("%#v", candidates)
	}
	mac := runningDesktopCandidates([]processInfo{{PID: 5, Path: "/Applications/Codex.app/Contents/MacOS/Codex"}, {PID: 6, ParentPID: 5, Path: "/Applications/Codex.app/Contents/Resources/codex"}})
	if len(mac) != 1 || mac[0].Path != "/Applications/Codex.app/Contents/Resources/codex" {
		t.Fatalf("%#v", mac)
	}
}
