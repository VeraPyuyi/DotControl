package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDownloadResumesPinnedHTTPSAndRejectsHashMismatch(t *testing.T) {
	body := []byte("a complete prebuilt payload")
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	var sawRange bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "bytes=7-" {
			t.Errorf("range=%q", r.Header.Get("Range"))
		}
		sawRange = true
		w.Header().Set("Content-Range", fmt.Sprintf("bytes 7-%d/%d", len(body)-1, len(body)))
		w.WriteHeader(http.StatusPartialContent)
		w.Write(body[7:])
	}))
	defer server.Close()
	base := t.TempDir()
	root := filepath.Join(base, "program")
	dir := filepath.Join(root, ".downloads")
	os.MkdirAll(dir, 0700)
	os.WriteFile(filepath.Join(dir, digest+".part"), body[:7], 0600)
	o := DownloadOptions{Root: root, StateDir: filepath.Join(base, "state"), URL: server.URL, SHA256: digest, Client: server.Client()}
	p, e := Download(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	got, e := os.ReadFile(p)
	if e != nil || string(got) != string(body) || !sawRange {
		t.Fatalf("result %q %v", got, e)
	}
	// A verified cached archive requires no network request.
	server.Close()
	if _, e = Download(context.Background(), o); e != nil {
		t.Fatal(e)
	}
	badServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("wrong")) }))
	defer badServer.Close()
	o.URL = badServer.URL
	o.Client = badServer.Client()
	o.SHA256 = strings.Repeat("0", 64)
	if _, e = Download(context.Background(), o); e == nil {
		t.Fatal("unpinned bytes accepted")
	}
	if _, e = os.Stat(filepath.Join(dir, o.SHA256+".part")); !os.IsNotExist(e) {
		t.Fatal("bad complete download left as resumable", e)
	}
}

func TestDownloadRefusesHTTPAndDowngrade(t *testing.T) {
	base := t.TempDir()
	o := DownloadOptions{Root: filepath.Join(base, "program"), StateDir: filepath.Join(base, "state"), URL: "http://127.0.0.1/", SHA256: strings.Repeat("0", 64)}
	if _, e := Download(context.Background(), o); e == nil {
		t.Fatal("HTTP accepted")
	}
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("followed insecure redirect") }))
	defer plain.Close()
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, plain.URL, http.StatusFound) }))
	defer secure.Close()
	o.URL = secure.URL
	o.Client = secure.Client()
	if _, e := Download(context.Background(), o); e == nil {
		t.Fatal("HTTPS downgrade accepted")
	}
}

func TestOversizedChunkedDownloadCanRetryFromZero(t *testing.T) {
	body := []byte("valid")
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	var calls int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Range") != "" {
			t.Errorf("invalid partial was resumed: %q", r.Header.Get("Range"))
		}
		if calls == 1 {
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush() // Force unknown Content-Length / chunked body.
			w.Write([]byte("0123456789"))
			return
		}
		w.Write(body)
	}))
	defer server.Close()
	base := t.TempDir()
	o := DownloadOptions{Root: filepath.Join(base, "program"), StateDir: filepath.Join(base, "state"), URL: server.URL, SHA256: digest, Client: server.Client(), MaxBytes: 8}
	if _, e := Download(context.Background(), o); e == nil {
		t.Fatal("oversized body accepted")
	}
	part := filepath.Join(o.Root, ".downloads", digest+".part")
	if _, e := os.Stat(part); !os.IsNotExist(e) {
		t.Fatalf("oversized partial remained after rejection: %v", e)
	}
	p, e := Download(context.Background(), o)
	if e != nil {
		t.Fatalf("retry could not recover: %v", e)
	}
	if got, e := os.ReadFile(p); e != nil || string(got) != string(body) {
		t.Fatalf("retried bytes %q %v", got, e)
	}
}

func TestPreexistingOversizedPartialIsResetForRetry(t *testing.T) {
	body := []byte("valid")
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			t.Errorf("oversized prefix resumed: %q", r.Header.Get("Range"))
		}
		w.Write(body)
	}))
	defer server.Close()
	base := t.TempDir()
	root := filepath.Join(base, "program")
	dir := filepath.Join(root, ".downloads")
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	part := filepath.Join(dir, digest+".part")
	if e := os.WriteFile(part, []byte("0123456789"), 0600); e != nil {
		t.Fatal(e)
	}
	o := DownloadOptions{Root: root, StateDir: filepath.Join(base, "state"), URL: server.URL, SHA256: digest, Client: server.Client(), MaxBytes: 8}
	if _, e := Download(context.Background(), o); e == nil {
		t.Fatal("oversized existing prefix was not reported")
	}
	if _, e := os.Stat(part); !os.IsNotExist(e) {
		t.Fatalf("invalid preexisting partial was not reset: %v", e)
	}
	if _, e := Download(context.Background(), o); e != nil {
		t.Fatalf("retry remained blocked: %v", e)
	}
}
