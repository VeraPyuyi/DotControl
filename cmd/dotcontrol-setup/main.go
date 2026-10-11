// DotControl Setup embeds a verified prebuilt payload in release builds.
package main

import (
	"archive/zip"
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/config"
	"github.com/dotcontrol/dotcontrol/internal/desktop"
	"github.com/dotcontrol/dotcontrol/internal/install"
	"github.com/dotcontrol/dotcontrol/internal/onboard"
)

//go:embed assets/*
var embedded embed.FS

//go:embed web/*
var web embed.FS

type setup struct {
	mu                              sync.Mutex
	dir, state, phase, problem, url string
	running                         bool
	choices                         []choice
}
type choice struct {
	Name     string `json:"name"`
	StateDir string `json:"state_dir"`
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--verify-payload" {
		if err := verifyPayload(); err != nil {
			fmt.Fprintln(os.Stderr, "payload_verification_failed")
			os.Exit(1)
		}
		return
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return
	}
	temp, e := os.MkdirTemp("", "DotControl-setup-")
	if e != nil {
		listener.Close()
		return
	}
	defer os.RemoveAll(temp) // exact MkdirTemp directory, never user data
	s := &setup{dir: temp, state: config.DefaultDir(), phase: "准备安装包"}
	assets, _ := fs.Sub(web, "web")
	ui := onboard.NewWithAssets(listener.Addr().String(), s, assets)
	srv := &http.Server{Handler: ui, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	defer srv.Close()
	go srv.Serve(listener)
	_ = desktop.OpenURL(ui.LaunchURL())
	go s.run()
	timer := time.NewTimer(45 * time.Minute)
	defer timer.Stop()
	<-timer.C
}

// Verifies the actual shipped bytes without installing, opening UI, or touching
// user configuration. Used by release download acceptance on each target OS.
func verifyPayload() error {
	temp, err := os.MkdirTemp("", "DotControl-verify-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	data, err := embedded.ReadFile("assets/payload.zip")
	if err != nil {
		return err
	}
	if err = extract(data, temp); err != nil {
		return err
	}
	manifest, err := install.Verify(context.Background(), temp)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"payload_verified": true, "version": manifest.Version, "os": manifest.OS, "arch": manifest.Arch, "files": len(manifest.Files)})
}
func (s *setup) Status(context.Context) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]any{"phase": s.phase, "error": s.problem, "running": s.running, "url": s.url, "instances": s.choices}, nil
}
func (s *setup) Connect(context.Context, onboard.Connect) error {
	return errors.New("unsupported_operation")
}
func (s *setup) Diagnostics(context.Context) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]string{"phase": s.phase, "error": s.problem}, nil
}
func (s *setup) Action(ctx context.Context, action string) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return nil, errors.New("installation_in_progress")
	}
	if strings.HasPrefix(action, "select:") {
		i, e := strconv.Atoi(strings.TrimPrefix(action, "select:"))
		if e != nil || i < 0 || i >= len(s.choices) {
			return nil, errors.New("invalid_instance")
		}
		s.state = s.choices[i].StateDir
	} else if action != "retry" {
		return nil, errors.New("unknown_action")
	}
	go s.run()
	return map[string]bool{"accepted": true}, nil
}
func (s *setup) set(phase, problem string) {
	s.mu.Lock()
	s.phase = phase
	s.problem = problem
	s.mu.Unlock()
}
func (s *setup) run() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.problem = ""
	state := s.state
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.running = false; s.mu.Unlock() }()
	bundle := filepath.Join(s.dir, "payload")
	if _, e := os.Stat(filepath.Join(bundle, "manifest.json")); e != nil {
		data, e := embedded.ReadFile("assets/payload.zip")
		if e != nil {
			s.set("安装包缺少组件", "这是开发构建，请下载 GitHub Release 的完整安装包。")
			return
		}
		s.set("校验并展开组件", "")
		if e = extract(data, bundle); e != nil {
			s.set("安装包校验失败", "请重新下载完整安装包；未更改已有设备。")
			return
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if _, e := install.Verify(ctx, bundle); e != nil {
		s.set("安装包校验失败", "组件摘要、系统或架构不匹配，请下载对应系统的完整安装包。")
		return
	}
	exe := filepath.Join(bundle, "dotcontrol")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	s.set("安装并检查本机代理", "")
	cmd := exec.CommandContext(ctx, exe, "--state", state, "repair", "--bundle", bundle)
	desktop.HideConsole(cmd)
	var out, errText bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errText
	if e := cmd.Run(); e != nil {
		var result struct {
			Instances []choice `json:"instances"`
		}
		if json.Unmarshal(out.Bytes(), &result) == nil && len(result.Instances) > 0 {
			s.mu.Lock()
			s.choices = result.Instances
			s.phase = "请选择要升级的已有设备"
			s.mu.Unlock()
			return
		}
		message := strings.TrimSpace(errText.String())
		if len(message) > 600 {
			message = "安装未通过健康检查，已尝试恢复旧版本。"
		}
		s.set("安装尚未完成", message)
		return
	}
	current, e := install.ReadCurrent(desktop.ProgramRoot())
	if e != nil {
		s.set("安装记录读取失败", "请重新打开安装包修复。")
		return
	}
	exe = filepath.Join(current.Dir, filepath.Base(exe))
	cmd = exec.CommandContext(ctx, exe, "--state", state, "onboard", "--json")
	desktop.HideConsole(cmd)
	data, e := cmd.Output()
	if e != nil {
		s.set("代理连接检查失败", "请重新打开安装包修复。已有身份和动作记录已保留。")
		return
	}
	var launch struct {
		URL string `json:"url"`
	}
	if json.Unmarshal(data, &launch) != nil || launch.URL == "" {
		s.set("向导未能打开", "请重新打开安装包。")
		return
	}
	s.mu.Lock()
	s.phase = "安装完成，正在进入连接向导"
	s.url = launch.URL
	s.mu.Unlock()
}
func extract(data []byte, dir string) error {
	z, e := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if e != nil {
		return e
	}
	if len(z.File) > 10000 {
		return errors.New("too_many_files")
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	var total uint64
	seen := map[string]bool{}
	for _, f := range z.File {
		name := strings.TrimSuffix(f.Name, "/")
		if name == "" || !fs.ValidPath(name) || strings.ContainsAny(name, `\:`) || f.Mode()&os.ModeSymlink != 0 {
			return errors.New("unsafe_archive")
		}
		key := strings.ToLower(name)
		if seen[key] {
			return errors.New("duplicate_archive_entry")
		}
		seen[key] = true
		total += f.UncompressedSize64
		if total > 2<<30 {
			return errors.New("archive_too_large")
		}
		target := filepath.Join(dir, filepath.FromSlash(name))
		if f.FileInfo().IsDir() {
			if e = os.MkdirAll(target, 0755); e != nil {
				return e
			}
			continue
		}
		if !f.Mode().IsRegular() {
			return errors.New("unsafe_archive_type")
		}
		if e = os.MkdirAll(filepath.Dir(target), 0755); e != nil {
			return e
		}
		r, e := f.Open()
		if e != nil {
			return e
		}
		w, e := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, f.Mode().Perm())
		if e != nil {
			r.Close()
			return e
		}
		_, e = io.Copy(w, io.LimitReader(r, int64(f.UncompressedSize64)+1))
		r.Close()
		closeErr := w.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
