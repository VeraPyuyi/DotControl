// Package tunnel supervises exactly one official tunnel-client per local run.
package tunnel

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/config"
	"github.com/dotcontrol/dotcontrol/internal/desktop"
	"github.com/gofrs/flock"
)

const Version = "0.0.16"

type Secrets interface{ Get(string) (string, error) }
type Supervisor struct {
	mu                   sync.Mutex
	Running              bool
	Ready                bool
	LastError            string
	cancel               context.CancelFunc
	done                 chan struct{}
	localReady           bool
	cloudState           string
	lastSuccessfulPollAt *time.Time
	proxySource          string
	network              config.Network
	networkChangePending bool
	networkChangeError   string
	networkWake          chan struct{}
	failureAt            time.Time
	networkRevision      uint64
}
type Status struct {
	Configured           bool       `json:"configured"`
	Running              bool       `json:"running"`
	Ready                bool       `json:"ready"`
	Error                string     `json:"error,omitempty"`
	LocalReady           bool       `json:"local_ready"`
	CloudState           string     `json:"cloud_state"`
	LastSuccessfulPollAt *time.Time `json:"last_successful_poll_at,omitempty"`
	ProxySource          string     `json:"proxy_source"`
	NetworkChangePending bool       `json:"network_change_pending"`
	NetworkChangeError   string     `json:"network_change_error,omitempty"`
}

func (s *Supervisor) applyHealth(h healthObservation, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.localReady = h.LocalReady
	s.cloudState = h.CloudState
	s.Ready = h.Ready
	if h.LastSuccessfulPollAt != nil {
		v := *h.LastSuccessfulPollAt
		s.lastSuccessfulPollAt = &v
	}
	if h.Error != "" {
		s.LastError = h.Error
		s.failureAt = now
		s.Ready = false
	} else if h.LastSuccessfulPollAt != nil && h.LastSuccessfulPollAt.After(s.failureAt) && (h.CloudState == "online" || h.CloudState == "backpressured") {
		s.LastError = ""
		s.failureAt = time.Time{}
	}
	if s.LastError != "" {
		s.Ready = false
	}
}

// UpdateNetwork queues a change on the existing lifecycle manager for the next
// child start. Version 0.0.16 has no lossless admission-freeze/drain protocol;
// health snapshots cannot authorize terminating a live child.
func (s *Supervisor) UpdateNetwork(n config.Network) error {
	if err := config.ValidateNetwork(n); err != nil {
		return err
	}
	if n.Mode == "" {
		n.Mode = "auto"
	}
	s.mu.Lock()
	s.network = n
	s.networkRevision++
	s.networkChangePending = true
	s.networkChangeError = ""
	wake := s.networkWake
	s.mu.Unlock()
	if wake != nil {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	return nil
}
func (s *Supervisor) networkConfig() (config.Network, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.network, s.networkRevision
}
func (s *Supervisor) observeNetwork(current, next resolvedNetwork) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.networkChangeError = ""
	if current.proxyURL == next.proxyURL {
		s.networkChangePending = false
		s.proxySource = next.Source
		return
	}
	s.networkChangePending = true
}

func (s *Supervisor) observeNetworkError(code string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.networkChangePending = true
	s.networkChangeError = code
}

// ControlPlaneEnvironment is shared by launch and doctor config checks. The
// returned arguments contain only an env reference; callers must never log env.
func ControlPlaneEnvironment(ctx context.Context, n config.Network, key, token string) ([]string, []string, error) {
	r, err := resolveNetwork(ctx, n, os.LookupEnv, readSystemProxy)
	if err != nil {
		return nil, nil, err
	}
	return r.environment(Environment(key, token)), r.arguments(), nil
}

func (s *Supervisor) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	var last *time.Time
	if s.lastSuccessfulPollAt != nil {
		value := *s.lastSuccessfulPollAt
		last = &value
	}
	return Status{Configured: s.done != nil, Running: s.Running, Ready: s.Ready, Error: s.LastError, LocalReady: s.localReady, CloudState: s.cloudState, LastSuccessfulPollAt: last, ProxySource: s.proxySource, NetworkChangePending: s.networkChangePending, NetworkChangeError: s.networkChangeError}
}
func (s *Supervisor) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.done != nil {
		<-s.done
	}
}
func Start(ctx context.Context, dir string, c config.Config, v Secrets) (*Supervisor, error) {
	if err := config.ValidateNetwork(c.Network); err != nil {
		return nil, err
	}
	s := &Supervisor{network: c.Network, networkWake: make(chan struct{}, 1), cloudState: "starting"}
	if c.TunnelID == "" {
		return s, nil
	}
	if c.TunnelPath == "" {
		return nil, errors.New("tunnel_binary_missing")
	}
	if key, e := v.Get("tunnel-key"); e != nil || key == "" {
		return nil, errors.New("tunnel_runtime_key_missing")
	}
	if token, e := v.Get("mcp-token"); e != nil || token == "" {
		return nil, errors.New("local_credential_missing")
	}
	lock := flock.New(filepath.Join(dir, "tunnel.lock"))
	ok, e := lock.TryLock()
	if e != nil || !ok {
		return nil, errors.New("tunnel_already_running")
	}
	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.done = make(chan struct{})
	go func() {
		defer close(s.done)
		defer lock.Unlock()
		delay := time.Second
		for ctx.Err() == nil {
			key, keyErr := v.Get("tunnel-key")
			token, tokenErr := v.Get("mcp-token")
			if keyErr != nil || key == "" || tokenErr != nil || token == "" {
				s.set(false, false, "tunnel_runtime_key_missing")
			} else {
				n, revision := s.networkConfig()
				resolved, err := resolveNetwork(ctx, n, os.LookupEnv, readSystemProxy)
				if err != nil {
					s.set(false, false, err.Error())
					s.mu.Lock()
					s.proxySource = resolved.Source
					s.cloudState = "proxy_blocked"
					s.mu.Unlock()
				} else {
					_, latestRevision := s.networkConfig()
					if revision != latestRevision {
						continue
					}
					ready := s.run(ctx, dir, c, key, token, resolved, revision)
					if ctx.Err() != nil {
						return
					}
					if ready {
						delay = time.Second
					}
				}
			}
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			case <-s.networkWake:
				timer.Stop()
			}
			if delay < 30*time.Second {
				delay *= 2
				if delay > 30*time.Second {
					delay = 30 * time.Second
				}
			}
		}
	}()
	return s, nil
}
func (s *Supervisor) set(running, ready bool, code string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Running = running
	s.Ready = ready
	s.localReady = false
	s.cloudState = "starting"
	if !running {
		s.cloudState = "stopped"
	}
	s.lastSuccessfulPollAt = nil
	if code != "" {
		s.LastError = code
		s.failureAt = time.Now()
	}
}

func (s *Supervisor) run(ctx context.Context, dir string, c config.Config, key, token string, resolved resolvedNetwork, revision uint64) bool {
	urlfile := filepath.Join(dir, "tunnel-health-"+config.Random()[:12]+".url")
	defer os.Remove(urlfile)
	args := []string{"run", "--control-plane.tunnel-id", c.TunnelID, "--control-plane.api-key", "env:DOTCONTROL_TUNNEL_KEY", "--mcp.server-url", "http://" + c.Listen + "/mcp", "--mcp.extra-headers", "X-DotControl-Token: env:DOTCONTROL_MCP_TOKEN", "--health.listen-addr", "127.0.0.1:0", "--health.url-file", urlfile, "--log.level", "warn", "--log.format", "json"}
	args = append(args, resolved.arguments()...)
	cmd := exec.CommandContext(ctx, c.TunnelPath, args...)
	desktop.HideConsole(cmd)
	cmd.Env = resolved.environment(Environment(key, token))
	cmd.Stdout = io.Discard
	// Raw logs can contain URLs, credentials and unrelated DNS/TLS substrings.
	// Only the bounded official health fields establish a failure category.
	cmd.Stderr = io.Discard
	generation := healthGeneration{launchedAt: time.Now()}
	if cmd.Start() != nil {
		s.set(false, false, "tunnel_start_failed")
		return false
	}
	s.set(true, false, "")
	s.mu.Lock()
	s.proxySource = resolved.Source
	s.networkChangePending = revision != s.networkRevision
	s.networkChangeError = ""
	s.mu.Unlock()
	processDone := make(chan error, 1)
	go func() { processDone <- cmd.Wait() }()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	everReady := false
	nextResolve := time.Now().Add(time.Minute)
	for {
		select {
		case <-processDone:
			s.mu.Lock()
			s.Running = false
			s.Ready = false
			s.localReady = false
			s.cloudState = "stopped"
			if ctx.Err() == nil && s.LastError == "" {
				s.LastError = "tunnel_process_exited"
				s.failureAt = time.Now()
			}
			s.mu.Unlock()
			return everReady
		case <-ticker.C:
			now := time.Now()
			observation := unavailable("tunnel_health_unavailable")
			if b, e := os.ReadFile(urlfile); e == nil {
				base := strings.TrimSpace(string(b))
				observation = generation.fetch(ctx, base, now)
			}
			s.applyHealth(observation, now)
			if s.Status().Ready {
				everReady = true
			}
			n, newRevision := s.networkConfig()
			if newRevision == revision && (n.Mode != "auto" && n.Mode != "" || now.Before(nextResolve)) && !s.Status().NetworkChangePending {
				continue
			}
			next, err := resolveNetwork(ctx, n, os.LookupEnv, readSystemProxy)
			nextResolve = now.Add(time.Minute)
			_, latestRevision := s.networkConfig()
			if newRevision != latestRevision {
				continue
			}
			if err != nil {
				s.observeNetworkError(err.Error())
				continue
			}
			// Observations are historical and non-atomic. In 0.0.16 "idle" is
			// published before received commands are enqueued; even a graceful
			// signal cancels active work. Preserve the child until it exits.
			s.observeNetwork(resolved, next)
			if resolved.proxyURL == next.proxyURL {
				resolved = next
				revision = newRevision
			}
		case <-s.networkWake:
			// Re-resolve on the next tick; a live child is never stopped here.
		}
	}
}
func Environment(key, token string) []string {
	allowed := map[string]bool{"PATH": true, "SYSTEMROOT": true, "WINDIR": true, "TEMP": true, "TMP": true, "TMPDIR": true, "HOME": true, "USERPROFILE": true, "APPDATA": true, "LOCALAPPDATA": true, "SSL_CERT_FILE": true, "SSL_CERT_DIR": true}
	result := []string{}
	for _, s := range os.Environ() {
		k, _, _ := strings.Cut(s, "=")
		if allowed[strings.ToUpper(k)] {
			result = append(result, s)
		}
	}
	return append(result, "DOTCONTROL_TUNNEL_KEY="+key, "DOTCONTROL_MCP_TOKEN="+token)
}
