package codexbridge

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"
)

func TestRPCErrorClassifiesOnlyActualWriterConflict(t *testing.T) {
	for _, tc := range []struct {
		wire string
		want bool
	}{
		{`{"code":-32600,"message":"thread owned-thread already has an active writer"}`, true},
		{`{"code":-32600,"message":"unknown permission profile; private path omitted"}`, false},
		{`{"code":-32603,"message":"thread owned-thread already has an active writer"}`, false},
	} {
		var err RPCError
		if e := json.Unmarshal([]byte(tc.wire), &err); e != nil {
			t.Fatal(e)
		}
		if err.LocalTakeover != tc.want {
			t.Fatalf("classification %v", err)
		}
		wire, _ := json.Marshal(err)
		if string(wire) != fmt.Sprintf(`{"Code":%d}`, err.Code) {
			t.Fatalf("private RPC text retained: %s", wire)
		}
	}
}

func TestRespondRequiresPendingRequestAndConsumesExactlyOnce(t *testing.T) {
	responses := make(chan rpcMessage, 1)
	c := fakeClient(t, func(conn net.Conn) {
		fmt.Fprintln(conn, `{"id":"review-1","method":"item/commandExecution/requestApproval","params":{"threadId":"owned","turnId":"turn-1"}}`)
		s := bufio.NewScanner(conn)
		if s.Scan() {
			var m rpcMessage
			json.Unmarshal(s.Bytes(), &m)
			responses <- m
		}
	})
	deadline := time.Now().Add(time.Second)
	for len(c.PendingRequests()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err := c.Respond(context.Background(), json.RawMessage(`"foreign"`), map[string]string{"decision": "accept"}); err == nil {
		t.Fatal("accepted unknown approval")
	}
	if err := c.Respond(context.Background(), json.RawMessage(`"review-1"`), map[string]string{"decision": "decline"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Respond(context.Background(), json.RawMessage(`"review-1"`), map[string]string{"decision": "accept"}); err == nil {
		t.Fatal("approval replayed")
	}
	select {
	case m := <-responses:
		if string(m.ID) != `"review-1"` || string(m.Result) != `{"decision":"decline"}` {
			t.Fatalf("unexpected response %+v", m)
		}
	case <-time.After(time.Second):
		t.Fatal("no response")
	}
}

func TestResolvedServerRequestCannotBeAnswered(t *testing.T) {
	c := fakeClient(t, func(conn net.Conn) {
		fmt.Fprintln(conn, `{"id":9,"method":"item/fileChange/requestApproval","params":{"threadId":"owned","turnId":"turn-1"}}`)
		fmt.Fprintln(conn, `{"method":"serverRequest/resolved","params":{"threadId":"owned","requestId":9}}`)
	})
	select {
	case <-c.Notifications():
	case <-time.After(time.Second):
		t.Fatal("no notification")
	}
	if len(c.PendingRequests()) != 0 {
		t.Fatal("resolved request remains pending")
	}
	if err := c.Respond(context.Background(), json.RawMessage(`9`), map[string]string{"decision": "accept"}); err == nil {
		t.Fatal("resolved approval accepted")
	}
}
