package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

type healthGeneration struct {
	launchedAt time.Time
	instanceID string
	startedAt  time.Time
}
type healthObservation struct {
	Ready, LocalReady, SafeToSwitch bool
	CloudState, Error               string
	LastSuccessfulPollAt            *time.Time
}
type healthComponent struct {
	Status     string          `json:"status"`
	State      string          `json:"state"`
	ReasonCode string          `json:"reason_code"`
	Limited    bool            `json:"limited"`
	Details    json.RawMessage `json:"details"`
}
type healthSnapshot struct {
	SchemaVersion int       `json:"schema_version"`
	Live          *bool     `json:"live"`
	Ready         *bool     `json:"ready"`
	SnapshotAt    time.Time `json:"snapshot_at"`
	Runtime       struct {
		InstanceID string    `json:"instance_id"`
		StartedAt  time.Time `json:"started_at"`
	} `json:"runtime"`
	Components map[string]healthComponent `json:"components"`
}
type pollDetails struct {
	LastAttempt           *time.Time `json:"last_attempt"`
	LastSuccess           *time.Time `json:"last_success"`
	LastError             *time.Time `json:"last_error"`
	ConsecutiveFailures   *uint64    `json:"consecutive_failures"`
	CurrentPollAgeSeconds *float64   `json:"current_poll_age_seconds"`
	DeadlineSeconds       *float64   `json:"deadline_seconds"`
	FailureCategory       string     `json:"failure_category"`
	HTTPStatus            int        `json:"http_status"`
}

func unavailable(code string) healthObservation {
	return healthObservation{CloudState: "unavailable", Error: code}
}
func (g *healthGeneration) observe(data []byte, now time.Time) healthObservation {
	bad := unavailable("tunnel_health_invalid")
	if len(data) > 65536 {
		return bad
	}
	var h healthSnapshot
	if json.Unmarshal(data, &h) != nil || h.SchemaVersion != 1 || h.Live == nil || !*h.Live || h.Ready == nil || h.Runtime.InstanceID == "" || len(h.Runtime.InstanceID) > 128 || h.Runtime.StartedAt.IsZero() || h.SnapshotAt.IsZero() {
		return bad
	}
	if h.Runtime.StartedAt.Before(g.launchedAt.UTC().Truncate(time.Second)) || h.Runtime.StartedAt.After(now.Add(2*time.Second)) {
		return unavailable("tunnel_health_generation_mismatch")
	}
	if g.instanceID != "" && (g.instanceID != h.Runtime.InstanceID || !g.startedAt.Equal(h.Runtime.StartedAt)) {
		return unavailable("tunnel_health_generation_mismatch")
	}
	if h.SnapshotAt.Before(h.Runtime.StartedAt) || h.SnapshotAt.After(now.Add(2*time.Second)) || now.Sub(h.SnapshotAt) > 10*time.Second {
		return bad
	}
	g.instanceID, g.startedAt = h.Runtime.InstanceID, h.Runtime.StartedAt
	bad.LocalReady = *h.Ready
	cp, ok := h.Components["control-plane"]
	if !ok || cp.Limited || len(cp.Details) == 0 || (cp.Status != "ok" && cp.Status != "unknown" && cp.Status != "degraded") {
		return bad
	}
	var p pollDetails
	if json.Unmarshal(cp.Details, &p) != nil || p.ConsecutiveFailures == nil || p.DeadlineSeconds == nil || *p.DeadlineSeconds <= 0 || *p.DeadlineSeconds > 600 || p.CurrentPollAgeSeconds == nil || *p.CurrentPollAgeSeconds < 0 {
		return bad
	}
	for _, ts := range []*time.Time{p.LastAttempt, p.LastSuccess, p.LastError} {
		if ts != nil && (ts.IsZero() || ts.Before(h.Runtime.StartedAt) || ts.After(h.SnapshotAt.Add(2*time.Second))) {
			return bad
		}
	}
	switch cp.State {
	case "starting", "polling", "idle", "backoff", "backpressured", "stopped":
	default:
		return bad
	}
	result := healthObservation{LocalReady: *h.Ready, CloudState: "starting", LastSuccessfulPollAt: p.LastSuccess, SafeToSwitch: snapshotIdle(h, cp.State)}
	failed := *p.ConsecutiveFailures > 0 || cp.Status == "degraded" || p.FailureCategory != "" || cp.ReasonCode != "" || (p.LastError != nil && (p.LastSuccess == nil || p.LastError.After(*p.LastSuccess)))
	if failed {
		result.CloudState = "degraded"
		result.Error = pollError(p.FailureCategory, cp.ReasonCode, p.HTTPStatus)
		return result
	}
	if p.LastSuccess == nil {
		return result
	}
	staleAfter := 90 * time.Second
	if d := time.Duration(*p.DeadlineSeconds * 2 * float64(time.Second)); d > staleAfter {
		staleAfter = d
	}
	if now.Sub(*p.LastSuccess) > staleAfter {
		result.CloudState = "unconfirmed"
		result.Error = "tunnel_cloud_evidence_stale"
		return result
	}
	if cp.Status != "ok" || cp.State == "starting" || cp.State == "stopped" || cp.State == "backoff" {
		return result
	}
	result.CloudState = "online"
	if cp.State == "backpressured" {
		result.CloudState = "backpressured"
	}
	result.Ready = result.LocalReady
	return result
}
func pollError(category, reason string, status int) string {
	if status == 407 {
		return "requires_local_proxy_auth"
	}
	if status == 401 {
		return "tunnel_credentials_rejected"
	}
	if status == 403 {
		return "tunnel_permission_denied"
	}
	if category == "" {
		category = reason
	}
	switch category {
	case "dns", "dns_error":
		return "tunnel_dns_failed"
	case "tls", "tls_error", "certificate":
		return "tunnel_tls_failed"
	case "timeout", "deadline", "deadline_exceeded":
		return "tunnel_network_timeout"
	case "canceled":
		return "tunnel_poll_canceled"
	case "http_status", "http":
		return "tunnel_http_failed"
	default:
		return "tunnel_network_failed"
	}
}
func snapshotIdle(h healthSnapshot, pollState string) bool {
	// A poll may still deliver work. Zero local counters alone are not enough.
	if pollState == "polling" || pollState == "backpressured" {
		return false
	}
	for _, field := range [][2]string{{"dispatcher", "active"}, {"response-delivery", "in_progress"}, {"queue", "depth"}} {
		c, ok := h.Components[field[0]]
		if !ok || c.Limited || len(c.Details) == 0 || (c.Status != "ok" && c.Status != "unknown" && c.Status != "degraded") {
			return false
		}
		var d map[string]json.RawMessage
		if json.Unmarshal(c.Details, &d) != nil {
			return false
		}
		raw, ok := d[field[1]]
		if !ok || string(raw) == "null" {
			return false
		}
		var count uint64
		if json.Unmarshal(raw, &count) != nil || count != 0 {
			return false
		}
	}
	return true
}
func (g *healthGeneration) fetch(ctx context.Context, base string, now time.Time) healthObservation {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.Port() == "" {
		return unavailable("tunnel_health_url_invalid")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return unavailable("tunnel_health_url_invalid")
	}
	u.Path = "/health"
	u.RawQuery = "details=true"
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := http.Client{Transport: transport, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect_rejected") }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return unavailable("tunnel_health_url_invalid")
	}
	response, err := client.Do(req)
	if err != nil {
		return unavailable("tunnel_health_unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return unavailable("tunnel_health_unavailable")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil {
		return unavailable("tunnel_health_invalid")
	}
	return g.observe(data, now)
}
