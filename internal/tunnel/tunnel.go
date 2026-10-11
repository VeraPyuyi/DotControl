// Package tunnel supervises exactly one official tunnel-client per local run.
package tunnel

import (
	"context"
	"errors"
	"io"
	"net/http"
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
	mu        sync.Mutex
	Running   bool
	Ready     bool
	LastError string
	cancel    context.CancelFunc
	done      chan struct{}
}
type Status struct {
	Configured bool   `json:"configured"`
	Running    bool   `json:"running"`
	Ready      bool   `json:"ready"`
	Error      string `json:"error,omitempty"`
}

func (s *Supervisor) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Status{Configured: s.done != nil, Running: s.Running, Ready: s.Ready, Error: s.LastError}
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
	s := &Supervisor{}
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
				ready := s.run(ctx, dir, c, key, token)
				if ctx.Err() != nil {
					return
				}
				if ready {
					delay = time.Second
				}
			}
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
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
	s.LastError = code
}

type classifiedLog struct{ s *Supervisor }

func (l classifiedLog) Write(p []byte) (int, error) {
	// Classify in memory and discard. Never retain a key-bearing log line.
	text := strings.ToLower(string(p))
	code := ""
	switch {
	case strings.Contains(text, "unauthorized") || strings.Contains(text, "invalid_api_key") || strings.Contains(text, "401"):
		code = "tunnel_credentials_rejected"
	case strings.Contains(text, "forbidden") || strings.Contains(text, "403"):
		code = "tunnel_permission_denied"
	case strings.Contains(text, "certificate") || strings.Contains(text, "x509") || strings.Contains(text, "tls"):
		code = "tunnel_tls_failed"
	case strings.Contains(text, "no such host") || strings.Contains(text, "enotfound") || strings.Contains(text, "dns"):
		code = "tunnel_dns_failed"
	case strings.Contains(text, "timed out") || strings.Contains(text, "timeout"):
		code = "tunnel_network_timeout"
	}
	if code != "" {
		l.s.mu.Lock()
		l.s.LastError = code
		l.s.mu.Unlock()
	}
	return len(p), nil
}
func (s *Supervisor) run(ctx context.Context, dir string, c config.Config, key, token string) bool {
	urlfile := filepath.Join(dir, "tunnel-health-"+config.Random()[:12]+".url")
	defer os.Remove(urlfile)
	args := []string{"run", "--control-plane.tunnel-id", c.TunnelID, "--control-plane.api-key", "env:DOTCONTROL_TUNNEL_KEY", "--mcp.server-url", "http://" + c.Listen + "/mcp", "--mcp.extra-headers", "X-DotControl-Token: env:DOTCONTROL_MCP_TOKEN", "--health.listen-addr", "127.0.0.1:0", "--health.url-file", urlfile, "--log.level", "warn", "--log.format", "json"}
	cmd := exec.CommandContext(ctx, c.TunnelPath, args...)
	desktop.HideConsole(cmd)
	cmd.Env = Environment(key, token)
	cmd.Stdout = io.Discard
	cmd.Stderr = classifiedLog{s}
	if cmd.Start() != nil {
		s.set(false, false, "tunnel_start_failed")
		return false
	}
	s.set(true, false, "")
	processDone := make(chan error, 1)
	go func() { processDone <- cmd.Wait() }()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	everReady := false
	for {
		select {
		case <-processDone:
			s.mu.Lock()
			s.Running = false
			s.Ready = false
			if ctx.Err() == nil && s.LastError == "" {
				s.LastError = "tunnel_process_exited"
			}
			s.mu.Unlock()
			return everReady
		case <-ticker.C:
			ready := false
			if b, e := os.ReadFile(urlfile); e == nil {
				base := strings.TrimSpace(string(b))
				if strings.HasPrefix(base, "http://127.0.0.1:") {
					client := http.Client{Timeout: 500 * time.Millisecond, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect_rejected") }}
					r, e := client.Get(strings.TrimRight(base, "/") + "/readyz")
					if e == nil {
						ready = r.StatusCode == 200
						r.Body.Close()
					}
				}
			}
			s.mu.Lock()
			s.Ready = ready
			if ready {
				s.LastError = ""
				everReady = true
			}
			s.mu.Unlock()
		}
	}
}
func Environment(key, token string) []string {
	allowed := map[string]bool{"PATH": true, "SYSTEMROOT": true, "WINDIR": true, "TEMP": true, "TMP": true, "TMPDIR": true, "HOME": true, "USERPROFILE": true, "APPDATA": true, "LOCALAPPDATA": true, "HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true, "SSL_CERT_FILE": true, "SSL_CERT_DIR": true}
	result := []string{}
	for _, s := range os.Environ() {
		k, _, _ := strings.Cut(s, "=")
		if allowed[strings.ToUpper(k)] {
			result = append(result, s)
		}
	}
	return append(result, "DOTCONTROL_TUNNEL_KEY="+key, "DOTCONTROL_MCP_TOKEN="+token)
}
