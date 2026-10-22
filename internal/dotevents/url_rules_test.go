package dotevents

import (
	"context"
	"strings"
	"testing"
)

func TestCallbackURLRuleDiagnostic(t *testing.T) {
	for _, tc := range []struct{ url, rule string }{{"https://example.com/" + strings.Repeat("a", 2048), "url_length"}, {"http://example.com/hook", "https_required"}, {"https://localhost/hook", "local_hostname"}, {"https://127.0.0.1/hook", "nonpublic_address"}} {
		t.Run(tc.rule, func(t *testing.T) {
			m, _ := openTestManager(t)
			p := testParams("alice")
			p.Delivery.URL = tc.url
			if _, err := m.Subscribe(context.Background(), "alice", p); err == nil {
				t.Fatal("invalid URL accepted")
			}
			st, err := m.Status("alice", "alice")
			if err != nil {
				t.Fatal(err)
			}
			if st.SubscribeFailure == nil || st.SubscribeFailure.Rule != tc.rule {
				t.Fatalf("diagnostic %#v want rule %s", st.SubscribeFailure, tc.rule)
			}
		})
	}
}
