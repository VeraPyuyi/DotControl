package dotevents

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// dohAnswer mirrors the subset of RFC 8484 JSON the resolver consumes.
type dohAnswer struct {
	Name string `json:"name"`
	Type int    `json:"type"`
	TTL  int    `json:"TTL"`
	Data string `json:"data"`
}

// dohResponse is the provider payload shape.
type dohResponse struct {
	Status int         `json:"status"`
	Answer []dohAnswer `json:"Answer"`
}

// dohFixture describes one provider's canned behavior.
type dohFixture struct {
	// records are returned verbatim, letting a test set a foreign Name.
	records []dohAnswer
	status  int
	// raw, when set, is written instead of encoding records.
	raw string
}

// newDoHServer builds a fixture DoH endpoint over TLS (the production client
// only accepts https). The returned client trusts every fixture's certificate.
func newDoHServer(t *testing.T, fx dohFixture) (*httptest.Server, string) {
	t.Helper()
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fx.raw != "" {
			w.Header().Set("Content-Type", "application/dns-json")
			_, _ = w.Write([]byte(fx.raw))
			return
		}
		w.Header().Set("Content-Type", "application/dns-json")
		_ = json.NewEncoder(w).Encode(dohResponse{Status: fx.status, Answer: fx.records})
	}))
	return s, s.URL
}

// aRecord builds a well-formed A record for the given host.
func aRecord(host, ip string) dohAnswer {
	return dohAnswer{Name: strings.ToLower(strings.TrimSuffix(host, ".")) + ".", Type: 1, TTL: 60, Data: ip}
}

// fixtureClient builds a client trusting the given fixtures. Empty is valid.
func fixtureClient(t *testing.T, servers ...*httptest.Server) *http.Client {
	t.Helper()
	pool := x509.NewCertPool()
	for _, s := range servers {
		pool.AddCert(s.Certificate())
	}
	return &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{
		Proxy:             nil,
		DisableKeepAlives: true,
		TLSClientConfig:   &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
	}}
}

// resolverFor builds a resolver over the given fixtures, with no fallback so a
// test cannot silently pass because the system resolver supplied an answer.
func resolverFor(t *testing.T, fixtures ...dohFixture) *dohResolver {
	t.Helper()
	servers := make([]*httptest.Server, 0, len(fixtures))
	urls := make([]string, 0, len(fixtures))
	for _, fx := range fixtures {
		s, u := newDoHServer(t, fx)
		servers = append(servers, s)
		urls = append(urls, u)
	}
	t.Cleanup(func() {
		for _, s := range servers {
			s.Close()
		}
	})
	return &dohResolver{
		providers: urls,
		budget:    2 * time.Second,
		client:    fixtureClient(t, servers...),
		fallback:  func(ctx context.Context, host string) ([]net.IPAddr, error) { return nil, context.DeadlineExceeded },
	}
}

// TestDoHResolverAcceptsDistinctPublicAnswers verifies the realistic case: two
// providers each return a different but public address (normal for CDNs). Both
// must be accepted rather than demanding byte-identical answers.
func TestDoHResolverAcceptsDistinctPublicAnswers(t *testing.T) {
	r := resolverFor(t,
		dohFixture{records: []dohAnswer{aRecord("chatgpt.com", "104.18.32.47")}},
		dohFixture{records: []dohAnswer{aRecord("chatgpt.com", "172.64.155.209")}},
	)
	got, err := r.lookup(context.Background(), "chatgpt.com")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %v, want both public answers", got)
	}
}

// TestDoHResolverIgnoresFakeIP verifies a provider answering with a fake-ip
// address is discarded, so the resolver can never smuggle 198.18.0.0/16 past the
// public-address guard.
func TestDoHResolverIgnoresFakeIP(t *testing.T) {
	r := resolverFor(t,
		dohFixture{records: []dohAnswer{aRecord("chatgpt.com", "198.18.0.21")}},
		dohFixture{records: []dohAnswer{aRecord("chatgpt.com", "104.18.32.47")}},
	)
	got, err := r.lookup(context.Background(), "chatgpt.com")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if len(got) != 1 || got[0].String() != "104.18.32.47" {
		t.Fatalf("got %v, want only the public answer", got)
	}
}

// TestDoHResolverRejectsEveryPrivateRange verifies no private, loopback,
// link-local, CGNAT, benchmark or special-use range can be returned by a
// provider. This is the core SSRF property.
func TestDoHResolverRejectsEveryPrivateRange(t *testing.T) {
	for _, ip := range []string{
		"198.18.0.21", "198.51.100.7", "10.0.0.1", "127.0.0.1", "169.254.169.254",
		"100.64.0.1", "192.0.2.1", "203.0.113.1", "192.168.1.1", "172.16.0.1",
		"0.0.0.0", "224.0.0.1", "255.255.255.255", "::1", "fe80::1", "fc00::1",
		"::ffff:198.18.0.21", "::ffff:127.0.0.1",
	} {
		t.Run(ip, func(t *testing.T) {
			r := resolverFor(t, dohFixture{records: []dohAnswer{aRecord("chatgpt.com", ip)}})
			if got, err := r.lookup(context.Background(), "chatgpt.com"); err == nil {
				t.Fatalf("provider answer %s returned %v, want rejection", ip, got)
			}
		})
	}
}

// TestDoHResolverAllFakeIPFails verifies that when every provider returns a
// non-public answer the resolver fails rather than returning it.
func TestDoHResolverAllFakeIPFails(t *testing.T) {
	r := resolverFor(t,
		dohFixture{records: []dohAnswer{aRecord("chatgpt.com", "198.18.0.21")}},
		dohFixture{records: []dohAnswer{aRecord("chatgpt.com", "198.18.0.22")}},
	)
	if got, err := r.lookup(context.Background(), "chatgpt.com"); err == nil {
		t.Fatalf("expected failure, got %v", got)
	}
}

// TestDoHResolverAllFakeIPIsNetworkNotCallback verifies the operator sees the
// truth: an all-fake-ip network condition must not be reported as a URL policy
// rejection.
func TestDoHResolverAllFakeIPIsNetworkNotCallback(t *testing.T) {
	r := resolverFor(t,
		dohFixture{records: []dohAnswer{aRecord("chatgpt.com", "198.18.0.21")}},
		dohFixture{records: []dohAnswer{aRecord("chatgpt.com", "198.18.0.22")}},
	)
	_, err := r.lookup(context.Background(), "chatgpt.com")
	if got := asEventsError(err).Code; got != "network" {
		t.Fatalf("code = %q, want network (not callback/url_rejected)", got)
	}
}

// TestDoHResolverRejectsForeignRecordName verifies a hostile provider cannot
// answer a query for the callback host with records for a different hostname,
// which would redirect the webhook and its secret to an attacker-chosen host.
func TestDoHResolverRejectsForeignRecordName(t *testing.T) {
	r := resolverFor(t, dohFixture{records: []dohAnswer{
		{Name: "evil.example.", Type: 1, TTL: 60, Data: "104.18.32.47"},
	}})
	if got, err := r.lookup(context.Background(), "chatgpt.com"); err == nil {
		t.Fatalf("foreign record name accepted: %v", got)
	}
}

// system resolver is used, so behavior is never worse than before the change.
func TestDoHResolverFallsBackToSystem(t *testing.T) {
	r := &dohResolver{
		providers: []string{"https://127.0.0.1:1/unreachable"},
		budget:    500 * time.Millisecond,
		client:    fixtureClient(t),
		fallback: func(ctx context.Context, host string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("104.18.32.47")}}, nil
		},
	}
	got, err := r.lookup(context.Background(), "chatgpt.com")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if len(got) != 1 || got[0].String() != "104.18.32.47" {
		t.Fatalf("got %v, want the fallback answer", got)
	}
}

// TestDoHResolverFallbackNonPublicIsRejected verifies the fallback path cannot
// bypass the public-address guard when the system resolver is hijacked, and that
// it is reported as a network condition rather than a URL policy rejection.
func TestDoHResolverFallbackNonPublicIsRejected(t *testing.T) {
	r := &dohResolver{
		providers: []string{"https://127.0.0.1:1/unreachable"},
		budget:    500 * time.Millisecond,
		client:    fixtureClient(t),
		fallback: func(ctx context.Context, host string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("198.18.0.21")}}, nil
		},
	}
	_, err := r.lookup(context.Background(), "chatgpt.com")
	if got := asEventsError(err).Code; got != "network" {
		t.Fatalf("code = %q, want network (a hijacked resolver is not a URL problem)", got)
	}
}

// TestDoHResolverFallbackMixedAnswersAreRejected verifies the all-or-nothing
// semantics: a fallback slice containing any non-public address is rejected
// whole, so one poisoned answer cannot slip through alongside a good one.
func TestDoHResolverFallbackMixedAnswersAreRejected(t *testing.T) {
	r := &dohResolver{
		providers: []string{"https://127.0.0.1:1/unreachable"},
		budget:    500 * time.Millisecond,
		client:    fixtureClient(t),
		fallback: func(ctx context.Context, host string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("104.18.32.47")}, {IP: net.ParseIP("127.0.0.1")}}, nil
		},
	}
	if got, err := r.lookup(context.Background(), "chatgpt.com"); err == nil {
		t.Fatalf("mixed fallback accepted: %v", got)
	}
}

// TestDoHResolverRejectsBadStatusAndOversized verifies provider failures are
// treated as "no answer" rather than poisoning the result.
func TestDoHResolverRejectsBadStatusAndOversized(t *testing.T) {
	r := resolverFor(t,
		dohFixture{status: 2},
		dohFixture{raw: `{"Status":0,"Answer":[{"name":"chatgpt.com.","type":1,"TTL":60,"data":"` + strings.Repeat("1", 4096) + `"}]}`},
	)
	if got, err := r.lookup(context.Background(), "chatgpt.com"); err == nil {
		t.Fatalf("expected failure, got %v", got)
	}
}

// TestDoHResolverRejectsNonHTTPSProvider verifies a plaintext or malformed
// provider URL is skipped rather than trusted.
func TestDoHResolverRejectsNonHTTPSProvider(t *testing.T) {
	for _, provider := range []string{"http://doh.example/dns-query", "not a url", "ftp://doh.example/dns-query", ""} {
		r := &dohResolver{
			providers: []string{provider},
			budget:    500 * time.Millisecond,
			client:    fixtureClient(t),
			fallback:  func(ctx context.Context, host string) ([]net.IPAddr, error) { return nil, context.DeadlineExceeded },
		}
		if got, err := r.lookup(context.Background(), "chatgpt.com"); err == nil {
			t.Fatalf("provider %q accepted: %v", provider, got)
		}
	}
}

// TestDoHResolverRejectsOversizedHost verifies the host length bound.
func TestDoHResolverRejectsOversizedHost(t *testing.T) {
	r := resolverFor(t, dohFixture{records: []dohAnswer{aRecord("chatgpt.com", "104.18.32.47")}})
	long := strings.Repeat("a.", 200) + "com"
	if got, err := r.lookup(context.Background(), long); err == nil {
		t.Fatalf("oversized host accepted: %v", got)
	}
}

// TestDoHResolverLiteralIPIsGuarded verifies the literal-address short path is
// itself policed, so it cannot be used to reach a private address.
func TestDoHResolverLiteralIPIsGuarded(t *testing.T) {
	r := resolverFor(t, dohFixture{records: []dohAnswer{aRecord("chatgpt.com", "104.18.32.47")}})
	for _, host := range []string{"127.0.0.1", "198.18.0.21", "10.0.0.1", "169.254.169.254", "::1"} {
		if got, err := r.lookup(context.Background(), host); err == nil {
			t.Fatalf("literal %q accepted: %v", host, got)
		}
	}
	// A public literal is returned unchanged.
	got, err := r.lookup(context.Background(), "104.18.32.47")
	if err != nil || len(got) != 1 || got[0].String() != "104.18.32.47" {
		t.Fatalf("public literal: got %v err %v", got, err)
	}
}

// TestDoHResolverBudgetIsShared verifies a single slow provider cannot consume
// the whole delivery budget: queries run concurrently under one deadline.
func TestDoHResolverBudgetIsShared(t *testing.T) {
	release := make(chan struct{})
	slow := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()
	defer close(release)
	fast, fastURL := newDoHServer(t, dohFixture{records: []dohAnswer{aRecord("chatgpt.com", "104.18.32.47")}})
	defer fast.Close()

	r := &dohResolver{
		providers: []string{slow.URL, fastURL},
		budget:    1 * time.Second,
		client:    fixtureClient(t, slow, fast),
		fallback:  func(ctx context.Context, host string) ([]net.IPAddr, error) { return nil, context.DeadlineExceeded },
	}
	start := time.Now()
	got, err := r.lookup(context.Background(), "chatgpt.com")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if len(got) != 1 || got[0].String() != "104.18.32.47" {
		t.Fatalf("got %v", got)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("took %v, want the shared 1s budget to bound the slow provider", elapsed)
	}
}

// TestDoHResolverCachesVerifiedAnswers verifies a burst of deliveries reuses one
// resolution instead of repeating the provider queries and TLS handshakes.
func TestDoHResolverCachesVerifiedAnswers(t *testing.T) {
	var queries atomic.Int64
	fixture := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries.Add(1)
		w.Header().Set("Content-Type", "application/dns-json")
		_ = json.NewEncoder(w).Encode(dohResponse{Answer: []dohAnswer{aRecord("chatgpt.com", "104.18.32.47")}})
	}))
	defer fixture.Close()

	r := &dohResolver{
		providers: []string{fixture.URL},
		budget:    2 * time.Second,
		client:    fixtureClient(t, fixture),
		fallback:  func(ctx context.Context, host string) ([]net.IPAddr, error) { return nil, context.DeadlineExceeded },
	}
	for i := 0; i < 5; i++ {
		got, err := r.lookup(context.Background(), "chatgpt.com")
		if err != nil || len(got) != 1 || got[0].String() != "104.18.32.47" {
			t.Fatalf("lookup %d: got %v err %v", i, got, err)
		}
	}
	if n := queries.Load(); n != 1 {
		t.Fatalf("provider queried %d times, want 1 (cached)", n)
	}
}

// TestDoHResolverDoesNotCacheRejections verifies a rejected host is retried
// rather than memoized, so a transient network failure is not made permanent.
func TestDoHResolverDoesNotCacheRejections(t *testing.T) {
	var queries atomic.Int64
	fixture := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries.Add(1)
		w.Header().Set("Content-Type", "application/dns-json")
		_ = json.NewEncoder(w).Encode(dohResponse{Answer: []dohAnswer{aRecord("chatgpt.com", "198.18.0.21")}})
	}))
	defer fixture.Close()

	r := &dohResolver{
		providers: []string{fixture.URL},
		budget:    2 * time.Second,
		client:    fixtureClient(t, fixture),
		fallback:  func(ctx context.Context, host string) ([]net.IPAddr, error) { return nil, context.DeadlineExceeded },
	}
	for i := 0; i < 3; i++ {
		if _, err := r.lookup(context.Background(), "chatgpt.com"); err == nil {
			t.Fatal("rejection was memoized and later returned an address")
		}
	}
	if n := queries.Load(); n != 3 {
		t.Fatalf("provider queried %d times, want 3 (no caching of failures)", n)
	}
}

// TestDoHResolverCacheReturnsCopy verifies the dial path cannot mutate a cached
// slice and corrupt later lookups.
func TestDoHResolverCacheReturnsCopy(t *testing.T) {
	fixture, fixtureURL := newDoHServer(t, dohFixture{records: []dohAnswer{aRecord("chatgpt.com", "104.18.32.47"), aRecord("chatgpt.com", "172.64.155.209")}})
	defer fixture.Close()
	r := &dohResolver{
		providers: []string{fixtureURL},
		budget:    2 * time.Second,
		client:    fixtureClient(t, fixture),
		fallback:  func(ctx context.Context, host string) ([]net.IPAddr, error) { return nil, context.DeadlineExceeded },
	}
	first, err := r.lookup(context.Background(), "chatgpt.com")
	if err != nil {
		t.Fatal(err)
	}
	first[0] = net.IPAddr{IP: net.ParseIP("127.0.0.1")}
	second, err := r.lookup(context.Background(), "chatgpt.com")
	if err != nil {
		t.Fatal(err)
	}
	if second[0].String() != "104.18.32.47" {
		t.Fatalf("cached slice was mutated: %v", second)
	}
}

// TestCallbackDialUsesDoHBeforeFakeIP verifies the end-to-end dial path: with a
// hijacked system resolver, DoH supplies the public address so verification
// proceeds instead of being rejected at the dial stage.
func TestCallbackDialUsesDoHBeforeFakeIP(t *testing.T) {
	doh, dohURL := newDoHServer(t, dohFixture{records: []dohAnswer{aRecord("chatgpt.com", "104.18.32.47")}})
	defer doh.Close()

	var dialed string
	r := &dohResolver{
		providers: []string{dohURL},
		budget:    2 * time.Second,
		client:    fixtureClient(t, doh),
		fallback: func(ctx context.Context, host string) ([]net.IPAddr, error) {
			// Simulates the hijacked system resolver (fake-ip mode).
			return []net.IPAddr{{IP: net.ParseIP("198.18.0.21")}}, nil
		},
	}
	d := NewDelivery(&http.Client{Transport: publicTransport(r.lookup, func(ctx context.Context, network, address string) (net.Conn, error) {
		dialed = address
		return nil, errf("dial_unreachable")
	})})
	err := d.verify(context.Background(), &subscriptionRecord{
		ID:     "sub.probe",
		Params: Params{Delivery: Callback{URL: "https://chatgpt.com/hook", Secret: testSecret()}},
		Secret: testSecret(),
	})
	if got := asEventsError(err).Code; got != "network" {
		t.Fatalf("code = %q, want network (dial attempted against the DoH address)", got)
	}
	if dialed != "104.18.32.47:443" {
		t.Fatalf("dialed %q, want the DoH-provided public address", dialed)
	}
}

// TestCallbackDialStillRejectsFakeIPAfterDoHFailure verifies that if DoH cannot
// help and the system resolver is hijacked, the guard still refuses the
// connection — the resolver must not have opened a bypass.
func TestCallbackDialStillRejectsFakeIPAfterDoHFailure(t *testing.T) {
	var dialed string
	r := &dohResolver{
		providers: []string{"https://127.0.0.1:1/unreachable"},
		budget:    500 * time.Millisecond,
		client:    fixtureClient(t),
		fallback: func(ctx context.Context, host string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("198.18.0.21")}}, nil
		},
	}
	d := NewDelivery(&http.Client{Transport: publicTransport(r.lookup, func(ctx context.Context, network, address string) (net.Conn, error) {
		dialed = address
		return nil, errf("dial_unreachable")
	})})
	err := d.verify(context.Background(), &subscriptionRecord{
		ID:     "sub.probe",
		Params: Params{Delivery: Callback{URL: "https://chatgpt.com/hook", Secret: testSecret()}},
		Secret: testSecret(),
	})
	if got := asEventsError(err).Code; got != "network" {
		t.Fatalf("code = %q, want network", got)
	}
	if dialed != "" {
		t.Fatalf("dialed %q, want no dial attempt", dialed)
	}
}

// TestDoHResolverFollowsCNAMEChain verifies the realistic CDN answer shape: a
// type-5 record followed by A records owned by the CNAME target. The addresses
// behind the chain must be returned, not discarded.
func TestDoHResolverFollowsCNAMEChain(t *testing.T) {
	r := resolverFor(t, dohFixture{records: []dohAnswer{
		{Name: "chatgpt.com.", Type: 5, TTL: 60, Data: "chatgpt.com.cdn.cloudflare.net."},
		{Name: "chatgpt.com.cdn.cloudflare.net.", Type: 1, TTL: 60, Data: "104.18.32.47"},
		{Name: "chatgpt.com.cdn.cloudflare.net.", Type: 1, TTL: 60, Data: "172.64.155.209"},
	}})
	got, err := r.lookup(context.Background(), "chatgpt.com")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %v, want the addresses behind the CNAME chain", got)
	}
}

// TestDoHResolverFollowsDeepCNAMEChain verifies a multi-hop chain, as returned
// by real providers (e.g. a 4-hop chain for outlook.office.com).
func TestDoHResolverFollowsDeepCNAMEChain(t *testing.T) {
	r := resolverFor(t, dohFixture{records: []dohAnswer{
		{Name: "outlook.office.com.", Type: 5, TTL: 60, Data: "outlook.cloud.microsoft."},
		{Name: "outlook.cloud.microsoft.", Type: 5, TTL: 60, Data: "acdcatm.outlook.mira.tm.svc.cloud.microsoft."},
		{Name: "acdcatm.outlook.mira.tm.svc.cloud.microsoft.", Type: 5, TTL: 60, Data: "shed.outlook.acdc.tm.svc.cloud.microsoft."},
		{Name: "shed.outlook.acdc.tm.svc.cloud.microsoft.", Type: 1, TTL: 60, Data: "40.104.72.18"},
	}})
	got, err := r.lookup(context.Background(), "outlook.office.com")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if len(got) != 1 || got[0].String() != "40.104.72.18" {
		t.Fatalf("got %v, want the terminal target address", got)
	}
}

// TestDoHResolverRejectsChainNotStartingAtHost verifies the security property
// that motivated name matching: a provider may not substitute an unrelated
// hostname, because the CNAME chain must begin at the queried host.
func TestDoHResolverRejectsChainNotStartingAtHost(t *testing.T) {
	r := resolverFor(t, dohFixture{records: []dohAnswer{
		{Name: "evil.example.", Type: 5, TTL: 60, Data: "target.example."},
		{Name: "target.example.", Type: 1, TTL: 60, Data: "104.18.32.47"},
	}})
	if got, err := r.lookup(context.Background(), "chatgpt.com"); err == nil {
		t.Fatalf("foreign chain accepted: %v", got)
	}
}

// TestDoHResolverRejectsBrokenCNAMEChain verifies a chain that does not terminate
// at an address is discarded rather than partially used.
func TestDoHResolverRejectsBrokenCNAMEChain(t *testing.T) {
	r := resolverFor(t, dohFixture{records: []dohAnswer{
		{Name: "chatgpt.com.", Type: 5, TTL: 60, Data: "missing.example."},
		{Name: "unrelated.example.", Type: 1, TTL: 60, Data: "104.18.32.47"},
	}})
	if got, err := r.lookup(context.Background(), "chatgpt.com"); err == nil {
		t.Fatalf("broken chain accepted: %v", got)
	}
}

// TestDoHResolverRejectsCNAMELoop verifies a cyclic chain cannot spin.
func TestDoHResolverRejectsCNAMELoop(t *testing.T) {
	r := resolverFor(t, dohFixture{records: []dohAnswer{
		{Name: "chatgpt.com.", Type: 5, TTL: 60, Data: "a.example."},
		{Name: "a.example.", Type: 5, TTL: 60, Data: "b.example."},
		{Name: "b.example.", Type: 5, TTL: 60, Data: "chatgpt.com."},
	}})
	if got, err := r.lookup(context.Background(), "chatgpt.com"); err == nil {
		t.Fatalf("CNAME loop accepted: %v", got)
	}
}

// TestDoHResolverCNAMEtoPrivateTargetIsRejected verifies a chain that leads to a
// non-public address is refused, so CNAME following cannot become an SSRF path.
func TestDoHResolverCNAMEtoPrivateTargetIsRejected(t *testing.T) {
	r := resolverFor(t, dohFixture{records: []dohAnswer{
		{Name: "chatgpt.com.", Type: 5, TTL: 60, Data: "internal.example."},
		{Name: "internal.example.", Type: 1, TTL: 60, Data: "127.0.0.1"},
	}})
	if got, err := r.lookup(context.Background(), "chatgpt.com"); err == nil {
		t.Fatalf("chain to a private target accepted: %v", got)
	}
}
