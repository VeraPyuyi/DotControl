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
	key, e := v.Get("tunnel-key")
	if e != nil || key == "" {
		return nil, errors.New("tunnel_runtime_key_missing")
	}
	token, e := v.Get("mcp-token")
	if e != nil {
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
	// An unpredictable health URL file prevents stale prior health from being
	// mistaken for this process. No runtime credentials are written to disk.
	urlfile := filepath.Join(dir, "tunnel-health-"+config.Random()[:12]+".url")
	args := []string{"run", "--control-plane.tunnel-id", c.TunnelID, "--control-plane.api-key", "env:DOTCONTROL_TUNNEL_KEY", "--mcp.server-url", "http://" + c.Listen + "/mcp", "--mcp.extra-headers", "X-DotControl-Token: env:DOTCONTROL_MCP_TOKEN", "--health.listen-addr", "127.0.0.1:0", "--health.url-file", urlfile, "--log.level", "warn", "--log.format", "json"}
	cmd := exec.CommandContext(ctx, c.TunnelPath, args...)
	cmd.Env = Environment(key, token)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if e = cmd.Start(); e != nil {
		cancel()
		lock.Unlock()
		return nil, errors.New("tunnel_start_failed")
	}
	s.Running = true
	go func() {
		defer close(s.done)
		defer lock.Unlock()
		defer os.Remove(urlfile)
		processDone := make(chan error, 1)
		go func() { processDone <- cmd.Wait() }()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case err := <-processDone:
				s.mu.Lock()
				s.Running = false
				s.Ready = false
				if ctx.Err() == nil && err != nil {
					s.LastError = "tunnel_exited_check_doctor"
				}
				s.mu.Unlock()
				return
			case <-ticker.C:
				ready := false
				if b, e := os.ReadFile(urlfile); e == nil {
					base := strings.TrimSpace(string(b))
					if strings.HasPrefix(base, "http://127.0.0.1:") {
						client := http.Client{Timeout: 500 * time.Millisecond}
						r, e := client.Get(strings.TrimRight(base, "/") + "/readyz")
						if e == nil {
							ready = r.StatusCode == 200
							r.Body.Close()
						}
					}
				}
				s.mu.Lock()
				s.Ready = ready
				s.mu.Unlock()
			}
		}
	}()
	return s, nil
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
