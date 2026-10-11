package codexbridge

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func fakeClient(t *testing.T, serve func(net.Conn)) *Client {
	t.Helper()
	a, b := net.Pipe()
	c := newClient(a, a, nil)
	t.Cleanup(func() { c.Close(); b.Close() })
	go serve(b)
	return c
}
func TestAppServerRequestHasNoRetryAndDisconnectIsUnknown(t *testing.T) {
	var calls atomic.Int32
	c := fakeClient(t, func(conn net.Conn) {
		s := bufio.NewScanner(conn)
		if s.Scan() {
			calls.Add(1)
		}
		conn.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var out map[string]any
	err := c.Call(ctx, "turn/start", map[string]any{}, &out)
	var transport *TransportError
	if !errors.As(err, &transport) || !transport.OutcomeUnknown || transport.Code != "codex_connection_lost" || calls.Load() != 1 {
		t.Fatalf("disconnect=%v calls=%d", err, calls.Load())
	}
}
func TestAppServerQueuesApprovalWithoutAnswerAndRoutesResponses(t *testing.T) {
	c := fakeClient(t, func(conn net.Conn) {
		s := bufio.NewScanner(conn)
		if !s.Scan() {
			return
		}
		var req rpcMessage
		json.Unmarshal(s.Bytes(), &req)
		fmt.Fprintln(conn, `{"id":"approval-1","method":"item/commandExecution/requestApproval","params":{"command":"private input"}}`)
		fmt.Fprintf(conn, "{\"id\":%s,\"result\":{\"ok\":true}}\n", req.ID)
		conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		if s.Scan() {
			t.Error("client automatically answered approval")
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var out struct {
		OK bool `json:"ok"`
	}
	if err := c.Call(ctx, "initialize", map[string]any{}, &out); err != nil || !out.OK {
		t.Fatalf("call=%+v %v", out, err)
	}
	requests := c.PendingRequests()
	if len(requests) != 1 || requests[0].Method != "item/commandExecution/requestApproval" {
		t.Fatalf("pending=%+v", requests)
	}
	requests[0].Params[0] = 'x'
	if c.PendingRequests()[0].Params[0] == 'x' {
		t.Fatal("pending request aliases internal state")
	}
}
func TestProbeUsesOnlyReadOnlyInventoryAndDoesNotClaimNativeAcceptance(t *testing.T) {
	methods := make(chan string, 10)
	c := fakeClient(t, func(conn net.Conn) {
		s := bufio.NewScanner(conn)
		for s.Scan() {
			var r rpcMessage
			if json.Unmarshal(s.Bytes(), &r) != nil {
				return
			}
			methods <- r.Method
			var result string
			switch r.Method {
			case "initialize":
				result = `{"userAgent":"codex/0.test"}`
			case "initialized":
				continue
			case "skills/list":
				result = `{"data":[{"skills":[{"name":"computer-use:computer-use","enabled":true,"path":"private/path"}]}]}`
			case "mcpServerStatus/list":
				result = `{"data":[{"name":"node_repl","tools":{"js":{}},"authStatus":"unsupported"}],"nextCursor":null}`
			default:
				t.Errorf("probe side effect: %s", r.Method)
				return
			}
			fmt.Fprintf(conn, "{\"id\":%s,\"result\":%s}\n", r.ID, result)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r, err := probe(ctx, c, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !r.Connection.Available || !r.ComputerUseSkillPresent || !r.NodeREPLPresent || r.OfficialComputerUse.Available || r.Delegation.Available {
		t.Fatalf("probe=%+v", r)
	}
	for _, want := range []string{"initialize", "initialized", "skills/list", "mcpServerStatus/list"} {
		if got := <-methods; got != want {
			t.Fatalf("method=%s want=%s", got, want)
		}
	}
}
func TestCallerTimeoutAfterSendIsUnknown(t *testing.T) {
	c := fakeClient(t, func(conn net.Conn) { bufio.NewScanner(conn).Scan() })
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	err := c.Call(ctx, "turn/start", map[string]any{}, nil)
	var transport *TransportError
	if !errors.As(err, &transport) || !transport.OutcomeUnknown || transport.Code != "codex_request_timeout" {
		t.Fatalf("timeout=%v", err)
	}
}

func TestConcurrentResponsesCorrelateWithoutSerializingRequests(t *testing.T) {
	c := fakeClient(t, func(conn net.Conn) {
		s := bufio.NewScanner(conn)
		var messages []rpcMessage
		for range 12 {
			if !s.Scan() {
				return
			}
			var m rpcMessage
			json.Unmarshal(s.Bytes(), &m)
			messages = append(messages, m)
		}
		for i := len(messages) - 1; i >= 0; i-- {
			fmt.Fprintf(conn, "{\"id\":%s,\"result\":%s}\n", messages[i].ID, messages[i].Params)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	errs := make(chan error, 12)
	for i := range 12 {
		go func() {
			var out struct {
				N int `json:"n"`
			}
			err := c.Call(ctx, "echo", map[string]int{"n": i}, &out)
			if err == nil && out.N != i {
				err = fmt.Errorf("response=%d want=%d", out.N, i)
			}
			errs <- err
		}()
	}
	for range 12 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
}

func TestRequestTimeoutClosesBlockedWriter(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	c := newClient(a, a, nil)
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	err := c.Call(ctx, "turn/start", map[string]any{}, nil)
	var transport *TransportError
	if !errors.As(err, &transport) || !transport.OutcomeUnknown {
		t.Fatalf("blocked write=%v", err)
	}
	select {
	case <-c.Done():
	default:
		t.Fatal("blocked transport still open")
	}
}

func TestMalformedResponseFailsClosedWithoutPrivateErrorText(t *testing.T) {
	c := fakeClient(t, func(conn net.Conn) {
		s := bufio.NewScanner(conn)
		if !s.Scan() {
			return
		}
		var m rpcMessage
		json.Unmarshal(s.Bytes(), &m)
		fmt.Fprintf(conn, "{\"id\":%s}\n", m.ID)
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := c.Call(ctx, "initialize", map[string]any{}, nil)
	if err == nil || err.Error() != "codex_protocol_error" {
		t.Fatalf("invalid envelope=%v", err)
	}
}
