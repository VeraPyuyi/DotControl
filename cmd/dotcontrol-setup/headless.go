package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/desktop"
	"github.com/dotcontrol/dotcontrol/internal/install"
)

type setupArgs struct {
	Install, NoStart             bool
	State, Root, Legacy, Extract string
}

func parseSetupArgs(args []string) (setupArgs, error) {
	var a setupArgs
	if len(args) > 16 {
		return a, errors.New("invalid_setup_arguments")
	}
	for _, arg := range args {
		if len(arg) > 32768 || strings.ContainsAny(arg, "\x00\r\n") {
			return a, errors.New("invalid_setup_arguments")
		}
	}
	f := flag.NewFlagSet("setup", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.BoolVar(&a.Install, "install", false, "")
	f.BoolVar(&a.NoStart, "no-start", false, "")
	f.StringVar(&a.State, "state", "", "")
	f.StringVar(&a.Root, "program-root", "", "")
	f.StringVar(&a.Legacy, "legacy-executable", "", "")
	f.StringVar(&a.Extract, "extract-payload", "", "")
	if e := f.Parse(args); e != nil || f.NArg() != 0 {
		return a, errors.New("invalid_setup_arguments")
	}
	if a.Extract != "" {
		if a.Install || a.NoStart || a.State != "" || a.Root != "" || a.Legacy != "" || !filepath.IsAbs(a.Extract) {
			return a, errors.New("invalid_setup_arguments")
		}
		return a, nil
	}
	if a.Install {
		if !filepath.IsAbs(a.State) || !filepath.IsAbs(a.Root) || (a.Legacy != "" && !filepath.IsAbs(a.Legacy)) {
			return a, errors.New("absolute_state_and_program_root_required")
		}
	} else if len(args) > 0 {
		return a, errors.New("invalid_setup_arguments")
	}
	return a, nil
}
func setupInstallArgs(a setupArgs, bundle string) []string {
	args := []string{"--state", a.State, "repair", "--bundle", bundle, "--program-root", a.Root}
	if a.NoStart {
		args = append(args, "--no-start")
	}
	if a.Legacy != "" {
		args = append(args, "--legacy-executable", a.Legacy)
	}
	return args
}
func payloadExecutable(bundle string) string {
	exe := filepath.Join(bundle, "dotcontrol")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	return exe
}
func preparePayloadDestination(dir string) (string, error) {
	canonical, e := install.CanonicalPath(dir)
	if e != nil {
		return "", errors.New("unsafe_payload_destination")
	}
	dir = canonical
	if entries, e := os.ReadDir(dir); e == nil && len(entries) > 0 {
		return "", errors.New("payload_destination_not_empty")
	} else if e != nil && !os.IsNotExist(e) {
		return "", e
	}
	return dir, nil
}
func extractVerifiedPayload(ctx context.Context, dir string) (install.Manifest, error) {
	dir, e := preparePayloadDestination(dir)
	if e != nil {
		return install.Manifest{}, e
	}
	data, e := embedded.ReadFile("assets/payload.zip")
	if e != nil {
		return install.Manifest{}, errors.New("embedded_payload_missing")
	}
	if e = extract(data, dir); e != nil {
		return install.Manifest{}, e
	}
	return install.Verify(ctx, dir)
}
func runHeadless(ctx context.Context, a setupArgs, out io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if a.Extract != "" {
		m, e := extractVerifiedPayload(ctx, a.Extract)
		if e != nil {
			return e
		}
		return json.NewEncoder(out).Encode(map[string]any{"payload_verified": true, "version": m.Version, "os": m.OS, "arch": m.Arch, "files": len(m.Files)})
	}
	temp, e := os.MkdirTemp("", "DotControl-install-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(temp)
	bundle := filepath.Join(temp, "payload")
	if _, e = extractVerifiedPayload(ctx, bundle); e != nil {
		return e
	}
	cmd := exec.CommandContext(ctx, payloadExecutable(bundle), setupInstallArgs(a, bundle)...)
	desktop.HideConsole(cmd)
	cmd.Stdout = out
	cmd.Stderr = os.Stderr
	if e = cmd.Run(); e != nil {
		return errors.New("embedded_install_failed")
	}
	return nil
}

func installationPhase(phase string) string {
	switch phase {
	case "download", "verify":
		return "校验安装组件"
	case "stage", "install":
		return "准备新版程序"
	case "waiting_for_exit":
		return "等待已有任务结束与旧进程退出"
	case "health":
		return "检查新版身份、版本与连接"
	case "cleaning_old_versions":
		return "新版已通过检查，清理旧程序"
	case "cleanup_pending":
		return "新版已启用，旧程序清理待重试"
	case "complete":
		return "安装检查完成"
	}
	return ""
}
