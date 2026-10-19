package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/dotcontrol/dotcontrol/internal/dotbridge"
)

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

func TestBridgeClientResponseEscapedResultComplete(t *testing.T) {
	body := strings.Repeat("x", 1573333)
	data, err := readBridgeResponseBody(strings.NewReader(body))
	if err != nil || len(data) != len(body) {
		t.Fatalf("escaped result: err=%v len=%d", err, len(data))
	}
}

func TestReadBridgeResponseBodyExactAndOverflowBudget(t *testing.T) {
	exact := bytes.Repeat([]byte{'a'}, dotbridge.MaxWireResponseBytes)
	data, err := readBridgeResponseBody(bytes.NewReader(exact))
	if err != nil || len(data) != dotbridge.MaxWireResponseBytes {
		t.Fatalf("exact 2MiB: err=%v len=%d", err, len(data))
	}
	over := append(exact, 'b')
	_, err = readBridgeResponseBody(bytes.NewReader(over))
	if err == nil || err.Error() != "bridge_response_too_large" {
		t.Fatalf("overflow: %v", err)
	}
}

func TestReadBridgeResponseBodyReadError(t *testing.T) {
	_, err := readBridgeResponseBody(errReader{errors.New("read_failed")})
	if err == nil {
		t.Fatal("expected read error")
	}
}

func TestBridgeClientResponseReadFailurePrintsNothing(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("hijack unavailable")
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Fatal(err)
		}
		_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\n"))
		_, _ = conn.Write([]byte("partial"))
		_ = conn.Close()
	}))
	srv.Start()
	defer srv.Close()
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	err := bridgeClientRequest(context.Background(), strings.TrimPrefix(srv.URL, "http://"), "fixture-token", http.MethodGet, "/dot-bridge/v1/jobs/job.1", nil)
	_ = w.Close()
	os.Stdout = old
	var printed strings.Builder
	_, _ = io.Copy(&printed, r)
	if err == nil || printed.String() != "" {
		t.Fatalf("read failure: err=%v out=%q", err, printed.String())
	}
}
