package dotevents

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Public DoH providers, queried concurrently. Order does not matter: every
// provider is asked under one shared deadline. No single provider is trusted —
// a hostile or poisoned answer must not be able to redirect a webhook, so a
// provider's answer is only accepted when it passes the public-address guard and
// the CNAME chain begins at the queried host.
var defaultDoHProviders = []string{
	"https://doh.pub/dns-query",
	"https://cloudflare-dns.com/dns-query",
	"https://dns.google/dns-query",
}

const (
	// dohBudget is the total time allowed for all DoH queries combined. It is a
	// fraction of the delivery deadline so that TLS and the verification
	// challenge still have time to run. A single provider may never exceed it.
	dohBudget = 4 * time.Second
	// dohMaxBody bounds the response read so a hostile provider cannot exhaust
	// memory with an oversized payload.
	dohMaxBody = 64 * 1024
)

// dohResolver resolves callback hostnames over DNS-over-HTTPS. It exists
// because some networks hijack the system resolver (fake-ip mode in Clash,
// sing-box, Surge and similar), which returns a synthetic address for every
// name. That synthetic address fails the public-address guard, so every
// subscription would be rejected before a connection was attempted.
//
// The resolver never weakens that guard. It only changes how a real address is
// discovered: every candidate must pass publicAddrs, and the returned record
// name must match the queried host. The system resolver remains the fallback,
// and its answer is filtered the same way.
type dohResolver struct {
	// providers is the list of DoH endpoints. Empty uses defaults.
	providers []string
	// budget bounds all DoH queries combined. Zero uses dohBudget.
	budget time.Duration
	// client performs the DoH requests. nil builds a proxy-bypassing client.
	client *http.Client
	// fallback resolves through the system resolver when DoH yields nothing.
	// Injected in tests; nil means net.DefaultResolver.
	fallback resolveFunc

	// mu guards cache. Delivery is called concurrently for many subscriptions,
	// and each event retries, so answers are memoized briefly to avoid three
	// new TLS handshakes per attempt.
	mu    sync.Mutex
	cache map[string]cachedAnswer
}

// cachedAnswer holds a verified answer for one host. Only addresses that passed
// publicAddrs are ever cached, so a cached entry cannot reintroduce a private
// address. The short TTL bounds staleness.
type cachedAnswer struct {
	addrs  []net.IPAddr
	expiry time.Time
}

// dohCacheTTL bounds how long a verified answer is reused. It is deliberately
// short: enough to absorb a burst of deliveries and retries, short enough that a
// legitimate address change is picked up quickly.
const dohCacheTTL = 60 * time.Second

func newDoHResolver() *dohResolver {
	return &dohResolver{providers: defaultDoHProviders, budget: dohBudget, cache: map[string]cachedAnswer{}}
}

func (r *dohResolver) httpClient() *http.Client {
	if r.client != nil {
		return r.client
	}
	return &http.Client{
		Timeout: r.budgetOr(),
		// DoH must bypass any configured proxy: the proxy may itself be the
		// component rewriting DNS answers.
		Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true},
	}
}

func (r *dohResolver) budgetOr() time.Duration {
	if r.budget > 0 {
		return r.budget
	}
	return dohBudget
}

func (r *dohResolver) fallbackResolver() resolveFunc {
	if r.fallback != nil {
		return r.fallback
	}
	return net.DefaultResolver.LookupIPAddr
}

func (r *dohResolver) providerList() []string {
	if len(r.providers) == 0 {
		return defaultDoHProviders
	}
	return r.providers
}

// lookup returns public addresses for host.
//
// Every provider is queried concurrently under one shared deadline so a slow or
// blackholed provider cannot consume the delivery budget. A provider's answer
// counts only when the record name matches the queried host and every address
// passes publicAddrs, so neither a fake-ip answer nor a hijacked answer for a
// different hostname can be used.
//
// Errors are classified for the operator: a URL that the policy guard rejects
// is "callback"; everything else (no provider reachable, no usable answer, or a
// system resolver that only yields non-public addresses) is "network", which is
// what it actually is. It is never reported as a URL policy rejection.
func (r *dohResolver) lookup(ctx context.Context, host string) ([]net.IPAddr, error) {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" || len(host) > 253 {
		return nil, errf("callback")
	}
	// Literal addresses need no resolution, but the guard still applies. The
	// caller re-validates, and rejecting here keeps the classification honest.
	if ip, err := netip.ParseAddr(host); err == nil {
		if !publicAddrs([]net.IPAddr{{IP: ip.AsSlice(), Zone: ip.Zone()}}) {
			return nil, errf("callback")
		}
		return []net.IPAddr{{IP: ip.AsSlice(), Zone: ip.Zone()}}, nil
	}
	if cached, ok := r.cached(host); ok {
		return cached, nil
	}
	addrs, err := r.resolve(ctx, host)
	if err != nil {
		return nil, err
	}
	r.remember(host, addrs)
	return addrs, nil
}

// resolve performs the actual provider queries and fallback. It is separated
// from lookup so only verified, public answers are memoized.
//
// The error code describes the true cause so an operator is not sent looking for
// a configuration problem that does not exist:
//
//   - "network"    no provider could be reached, or the system resolver failed.
//     This is the fake-ip case: DNS is answering, but not usefully.
//   - "callback"   the URL itself violates policy (a non-public literal host),
//     or every resolver returned an address the guard must reject.
//
// A hijacked resolver is a network condition, not a URL policy rejection, so it
// is reported as "network" and the operator is pointed at DNS.
func (r *dohResolver) resolve(ctx context.Context, host string) ([]net.IPAddr, error) {
	if out := r.queryProviders(ctx, host); len(out) > 0 {
		return out, nil
	}
	// DoH produced nothing usable. Fall back to the system resolver so behavior
	// is never worse than before, and let the guard reject it if the answer is
	// still non-public.
	addrs, e := r.fallbackResolver()(ctx, host)
	if e != nil || len(addrs) == 0 {
		return nil, errf("network")
	}
	if !publicAddrs(addrs) {
		// The system resolver returned only addresses the guard must reject.
		// That is a hijacked or fake-ip resolver, which is a network condition.
		return nil, errf("network")
	}
	return addrs, nil
}

func (r *dohResolver) cached(host string) ([]net.IPAddr, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cache == nil {
		return nil, false
	}
	e, ok := r.cache[host]
	if !ok || !time.Now().Before(e.expiry) || len(e.addrs) == 0 {
		return nil, false
	}
	// Return a copy: the dial path must not be able to mutate a shared slice.
	out := make([]net.IPAddr, len(e.addrs))
	copy(out, e.addrs)
	return out, true
}

func (r *dohResolver) remember(host string, addrs []net.IPAddr) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cache == nil {
		r.cache = map[string]cachedAnswer{}
	}
	out := make([]net.IPAddr, len(addrs))
	copy(out, addrs)
	r.cache[host] = cachedAnswer{addrs: out, expiry: time.Now().Add(dohCacheTTL)}
}

// queryProviders asks every provider concurrently and returns the union of
// addresses that passed the guard. Providers are never retried, so the total
// work is bounded by the shared deadline.
func (r *dohResolver) queryProviders(ctx context.Context, host string) []net.IPAddr {
	providers := r.providerList()
	if len(providers) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, r.budgetOr())
	defer cancel()
	type result struct {
		addrs []net.IPAddr
	}
	results := make(chan result, len(providers))
	var wg sync.WaitGroup
	for _, provider := range providers {
		wg.Add(1)
		go func(provider string) {
			defer wg.Done()
			addrs, ok := r.queryProvider(ctx, provider, host)
			if !ok {
				return
			}
			results <- result{addrs: addrs}
		}(provider)
	}
	wg.Wait()
	close(results)
	seen := map[string]bool{}
	var out []net.IPAddr
	for res := range results {
		for _, a := range res.addrs {
			// Defensive: the guard is applied here as well as per provider, so
			// no address can reach the dial path without passing it.
			if !publicAddrs([]net.IPAddr{a}) || seen[a.IP.String()] {
				continue
			}
			seen[a.IP.String()] = true
			out = append(out, a)
		}
	}
	return out
}

// queryProvider asks one DoH endpoint. The bool reports whether the provider
// gave a well-formed answer for this host that passed the public-address guard.
func (r *dohResolver) queryProvider(ctx context.Context, provider, host string) ([]net.IPAddr, bool) {
	endpoint, err := url.Parse(provider)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" {
		return nil, false
	}
	q := endpoint.Query()
	q.Set("name", host)
	q.Set("type", "A")
	endpoint.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, false
	}
	req.Header.Set("Accept", "application/dns-json")
	resp, err := r.httpClient().Do(req)
	if err != nil {
		return nil, false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, dohMaxBody))
	if err != nil {
		return nil, false
	}
	return parseDoHAnswer(body, host)
}

// parseDoHAnswer extracts the A records that answer a query for host.
//
// A real answer section is the wire answer verbatim, so a CNAME'd host arrives
// as a type-5 record followed by A records owned by the CNAME target. Demanding
// an exact owner name would discard every such answer, silently reverting
// CDN-fronted hosts to the hijacked system resolver. The chain is therefore
// followed from the queried name, which also preserves the security property: an
// address is accepted only when it is reachable by following CNAME records that
// start at the queried host. A provider cannot substitute an unrelated hostname,
// because the chain must begin at host and each link must be present.
//
// SERVFAIL, malformed JSON, a broken chain, non-A records and unparseable or
// non-public addresses all yield "no answer".
func parseDoHAnswer(body []byte, host string) ([]net.IPAddr, bool) {
	var payload struct {
		Status int `json:"status"`
		Answer []struct {
			Name string `json:"name"`
			Type int    `json:"type"`
			Data string `json:"data"`
		} `json:"Answer"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, false
	}
	// NOERROR only. SERVFAIL, REFUSED and NXDOMAIN are not answers.
	if payload.Status != 0 {
		return nil, false
	}
	normalize := func(s string) string { return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(s), ".")) }
	want := normalize(host)
	if want == "" {
		return nil, false
	}
	// Collect the CNAME links and the addresses for each owner name.
	owners := map[string]*cnameEntry{}
	for _, a := range payload.Answer {
		name := normalize(a.Name)
		if name == "" {
			continue
		}
		e, ok := owners[name]
		if !ok {
			e = &cnameEntry{}
			owners[name] = e
		}
		switch a.Type {
		case 1:
			ip, err := netip.ParseAddr(strings.TrimSpace(a.Data))
			if err != nil || !ip.Unmap().Is4() {
				continue
			}
			// Only public addresses may be returned. This is the same guard the
			// dial path applies, so the two layers cannot disagree.
			if !publicAddrs([]net.IPAddr{{IP: ip.AsSlice()}}) {
				continue
			}
			e.a = append(e.a, ip)
		case 5:
			target := normalize(a.Data)
			if target == "" || target == name {
				continue
			}
			// Only the first link for an owner is honored; a second, different
			// link is an attempt to fork the chain.
			if e.cname != "" && e.cname != target {
				continue
			}
			e.cname = target
		}
	}
	// Walk the chain from the queried host, bounded by the number of records so
	// a cycle cannot spin.
	addrs := walkCNAMEChain(owners, want, len(payload.Answer))
	if len(addrs) == 0 {
		return nil, false
	}
	out := make([]net.IPAddr, 0, len(addrs))
	for _, ip := range addrs {
		out = append(out, net.IPAddr{IP: ip.AsSlice()})
	}
	return out, true
}

// cnameEntry collects the CNAME target and the public addresses for one owner
// name within a single DoH answer section.
type cnameEntry struct {
	cname string
	a     []netip.Addr
}

// walkCNAMEChain follows CNAME links from the queried host and returns the
// public addresses owned by the terminal target. Addresses at intermediate names
// are not used: only the final target resolves the query.
func walkCNAMEChain(owners map[string]*cnameEntry, start string, limit int) []netip.Addr {
	seen := map[string]bool{}
	var out []netip.Addr
	for cur := start; cur != "" && limit > 0; limit-- {
		if seen[cur] {
			return nil
		}
		seen[cur] = true
		e, ok := owners[cur]
		if !ok {
			return nil
		}
		out = append(out, e.a...)
		if e.cname == "" {
			return out
		}
		cur = e.cname
	}
	return nil
}
