package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

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
