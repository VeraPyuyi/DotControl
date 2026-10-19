package dotevents

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
)

func TestReservedGuardCoverage(t *testing.T) {
	for _, s := range []string{"0.1.2.3", "192.0.0.8", "192.88.99.1", "198.19.0.1", "2001::1", "2002:a00:1::", "3fff::1", "4000::1", "203.0.113.10"} {
		if publicIP(netip.MustParseAddr(s)) {
			t.Errorf("reserved IP accepted %s", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if !publicIP(netip.MustParseAddr(s)) {
			t.Errorf("public IP rejected %s", s)
		}
	}
}
func TestProductionDialGuardRebindingAndAllAnswers(t *testing.T) {
	calls, dials := 0, 0
	resolve := func(context.Context, string) ([]net.IPAddr, error) {
		calls++
		if calls == 1 {
			return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
		}
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("127.0.0.1")}}, nil
	}
	dial := func(_ context.Context, _, address string) (net.Conn, error) {
		dials++
		if address != "8.8.8.8:443" {
			t.Errorf("dial not pinned: %s", address)
		}
		return nil, errors.New("offline")
	}
	tr := publicTransport(resolve, dial)
	if tr.Proxy != nil {
		t.Fatal("environment proxy enabled")
	}
	_, _ = tr.DialContext(context.Background(), "tcp", "callback.example:443")
	_, _ = tr.DialContext(context.Background(), "tcp", "callback.example:443")
	if calls != 2 || dials != 1 {
		t.Fatalf("resolver=%d dial=%d", calls, dials)
	}
	for _, s := range []string{"127.0.0.1:443", "[::1]:443", "169.254.169.254:443", "203.0.113.10:443"} {
		_, _ = tr.DialContext(context.Background(), "tcp", s)
	}
	if dials != 1 {
		t.Fatal("private literal reached socket")
	}
}
func TestStrictParamsJSON(t *testing.T) {
	for _, s := range []string{
		`{"name":"dot.task.available","name":"other"}`,
		`{"name":"dot.task.available","arguments":{"queue_id":"alice","extra":true}}`,
		`{"name":"dot.task.available","arguments":{"queue_id":null}}`,
		`{"Name":"dot.task.available"}`,
		`{"name":"dot.task.available","delivery":{"mode":"webhook","url":"https://callback.example","secret":"x","extra":1}}`,
	} {
		var p Params
		if e := p.UnmarshalJSON([]byte(s)); e == nil {
			t.Errorf("accepted invalid schema %s", s)
		}
	}
}
