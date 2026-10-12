package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyUpgradeExplainsNormalExitAndRetry(t *testing.T) {
	message := installationMessage("DotControl: legacy_agent_stop_required\n")
	// Activation and recovery can both encounter the same still-running agent.
	if got := installationMessage("DotControl: legacy_agent_stop_required\nlegacy_agent_stop_required\n"); got != message {
		t.Fatalf("joined migration/recovery error lost instructions: %s", got)
	}
	for _, step := range []string{"不支持自动退出", "Control+C", "重试安装", "无需删除锁文件"} {
		if !strings.Contains(message, step) {
			t.Fatalf("missing step %q: %s", step, message)
		}
	}
	for _, code := range []string{"agent_stop_timeout", "wait_for_active_task_to_stop"} {
		if !strings.Contains(installationMessage("DotControl: "+code), "不一定表示正在执行桌面任务") {
			t.Fatal("lifetime lock incorrectly described as an active task")
		}
	}
}

func archive(t *testing.T, names ...string) []byte {
	t.Helper()
	var data bytes.Buffer
	z := zip.NewWriter(&data)
	for _, name := range names {
		w, e := z.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		w.Write([]byte("test"))
	}
	z.Close()
	return data.Bytes()
}
func TestExtractRejectsTraversalAndCaseAliases(t *testing.T) {
	for _, names := range [][]string{{"../escape"}, {"C:/escape"}, {"a", "A"}, {"a/../../escape"}, {`a\..\escape`}} {
		if extract(archive(t, names...), filepath.Join(t.TempDir(), "payload")) == nil {
			t.Fatal("unsafe archive accepted", names)
		}
	}
}
func TestExtractUnicodePathAndNoOverwrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "中文 路径")
	data := archive(t, "folder/文件.txt")
	if e := extract(data, dir); e != nil {
		t.Fatal(e)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "folder", "文件.txt"))
	if string(got) != "test" {
		t.Fatal("content differs")
	}
	if extract(data, dir) == nil {
		t.Fatal("existing files overwritten")
	}
}
