package codexbridge

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"
)

func TestPermissionProfilesReadAllPagesWithoutCreatingThreads(t *testing.T) {
	count := 0
	c := fakeClient(t, func(conn net.Conn) {
		s := bufio.NewScanner(conn)
		for s.Scan() {
			var m rpcMessage
			json.Unmarshal(s.Bytes(), &m)
			if m.Method != "permissionProfile/list" {
				t.Errorf("unexpected %s", m.Method)
				return
			}
			count++
			data := `{"data":[{"id":"restricted","name":"Restricted"}],"nextCursor":"next"}`
			if count == 2 {
				data = `{"data":[{"id":"project","name":"Project"}],"nextCursor":null}`
			}
			fmt.Fprintf(conn, "{\"id\":%s,\"result\":%s}\n", m.ID, data)
		}
	})
	profiles, err := ReadPermissionProfiles(context.Background(), c, t.TempDir())
	if err != nil || len(profiles) != 2 {
		t.Fatalf("%v %v", profiles, err)
	}
}
