package codexbridge

import (
	"encoding/json"
	"fmt"
	"testing"
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
