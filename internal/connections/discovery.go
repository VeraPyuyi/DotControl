package connections

import (
	"context"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	SourceConfigured     = "configured"
	SourceRunningDesktop = "desktop_running"
	SourceBundled        = "desktop_bundled"
	SourcePATH           = "path_cli"
)

// Candidate paths are local-only configuration input, not diagnostics.
type Candidate struct {
	Path   string `json:"-"`
	Source string `json:"source"`
}
type DiscoveryOptions struct {
	Candidates func(context.Context) ([]Candidate, error)
	Version    func(context.Context, string) (string, error)
}
type DiscoveryResult struct {
	Status
	Executable string `json:"-"`
	Source     string `json:"source,omitempty"`
	Version    string `json:"version,omitempty"`
}

var codexVersion = regexp.MustCompile(`(?m)^codex(?:-cli)? ([0-9][0-9A-Za-z.+_-]{0,63})(?:\s|$)`)

// DiscoverCodex honors an explicit path. Without one, it prefers running
// desktop-child executables, then installed desktop resources, then PATH CLI.
// Discovery uses only executable paths and process ancestry, never auth files,
// private app configuration, process command lines, or conversation data.
// Source indicates provenance from path/ancestry, not a signature attestation.
func DiscoverCodex(ctx context.Context, configured string, opts DiscoveryOptions) (DiscoveryResult, error) {
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	fail := func(code string) (DiscoveryResult, error) {
		return DiscoveryResult{Status: Status{Code: code}}, failure(code)
	}
	var candidates []Candidate
	if configured != "" {
		if !absoluteLocal(configured) {
			return fail("invalid_codex_executable")
		}
		candidates = []Candidate{{Path: configured, Source: SourceConfigured}}
	} else {
		discover := opts.Candidates
		if discover == nil {
			discover = defaultCandidates
		}
		var err error
		candidates, err = discover(bounded)
		if err != nil {
			return fail("codex_discovery_unavailable")
		}
	}
	rank := map[string]int{SourceConfigured: 0, SourceRunningDesktop: 1, SourceBundled: 2, SourcePATH: 3}
	sort.SliceStable(candidates, func(i, j int) bool { return rank[candidates[i].Source] < rank[candidates[j].Source] })
	version := opts.Version
	if version == nil {
		version = func(ctx context.Context, path string) (string, error) {
			result, err := runCommand(ctx, path, []string{"--version"})
			return result.Stdout, err
		}
	}
	seen := map[string]bool{}
	probed := false
	for _, candidate := range candidates {
		if _, ok := rank[candidate.Source]; !ok || !absoluteLocal(candidate.Path) || seen[candidate.Path] {
			continue
		}
		if configured == "" && candidate.Source == SourceConfigured {
			continue
		}
		if bounded.Err() != nil {
			return fail("codex_discovery_timeout")
		}
		seen[candidate.Path] = true
		probed = true
		probeCtx, probeCancel := context.WithTimeout(bounded, 3*time.Second)
		output, err := version(probeCtx, candidate.Path)
		probeCancel()
		if err != nil {
			continue
		}
		matched := codexVersion.FindStringSubmatch(output)
		if len(matched) != 2 {
			continue
		}
		return DiscoveryResult{Status: Status{Available: true, Code: "codex_found"}, Executable: candidate.Path, Source: candidate.Source, Version: matched[1]}, nil
	}
	if bounded.Err() != nil {
		return fail("codex_discovery_timeout")
	}
	if probed {
		return fail("codex_version_unavailable")
	}
	return fail("codex_not_found")
}

func defaultCandidates(ctx context.Context) ([]Candidate, error) {
	processes, _ := listProcesses(ctx)
	candidates := runningDesktopCandidates(processes)
	for _, path := range bundledPaths(processes) {
		candidates = append(candidates, Candidate{Path: path, Source: SourceBundled})
	}
	if path, err := exec.LookPath("codex"); err == nil {
		candidates = append(candidates, Candidate{Path: path, Source: SourcePATH})
	}
	return candidates, nil
}

type processInfo struct {
	PID, ParentPID uint32
	Path           string
}

func normalizedPath(path string) string { return strings.ToLower(strings.ReplaceAll(path, `\`, "/")) }
func desktopApp(path string) bool {
	p := normalizedPath(path)
	return strings.Contains(p, "/windowsapps/openai.codex_") && (strings.HasSuffix(p, "/app/chatgpt.exe") || strings.HasSuffix(p, "/app/codex.exe")) || strings.HasSuffix(p, "/codex.app/contents/macos/codex")
}
func desktopCLI(path string) bool {
	p := normalizedPath(path)
	return strings.HasSuffix(p, "/codex.exe") && (strings.Contains(p, "/openai/codex/bin/") || strings.Contains(p, "/windowsapps/openai.codex_") && strings.Contains(p, "/app/")) || strings.Contains(p, "/codex.app/contents/resources/") && strings.HasSuffix(p, "/codex") || strings.Contains(p, "/application support/codex/bin/") && strings.HasSuffix(p, "/codex")
}
func runningDesktopCandidates(processes []processInfo) []Candidate {
	byPID := map[uint32]processInfo{}
	for _, p := range processes {
		byPID[p.PID] = p
	}
	var candidates []Candidate
	for _, p := range processes {
		if !desktopCLI(p.Path) {
			continue
		}
		parent := p.ParentPID
		visited := map[uint32]bool{}
		for depth := 0; parent != 0 && depth < 32 && !visited[parent]; depth++ {
			visited[parent] = true
			ancestor, ok := byPID[parent]
			if !ok {
				break
			}
			if desktopApp(ancestor.Path) {
				candidates = append(candidates, Candidate{Path: p.Path, Source: SourceRunningDesktop})
				break
			}
			parent = ancestor.ParentPID
		}
	}
	return candidates
}
