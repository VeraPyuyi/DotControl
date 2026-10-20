package dotevents

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

const (
	maxHTTPTimeout    = 10 * time.Second
	maxResponseBytes  = 16 * 1024
	maxEventBodyBytes = 256 * 1024
	secretRotateGrace = 60 * time.Second
)

var retrySchedule = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}

// Delivery's zero value uses the public HTTPS transport. Test fixtures may
// supply a custom HTTP client through NewDelivery.
type Delivery struct{ client *http.Client }

// NewDelivery constructs a delivery transport with an explicit HTTP client.
// Production callers should pass nil to use the public HTTPS transport.
func NewDelivery(client *http.Client) *Delivery {
	if client == nil {
		return &Delivery{}
	}
	return &Delivery{client: client}
}

func (d *Delivery) httpClient() *http.Client {
	if d != nil && d.client != nil {
		return d.client
	}
	return &http.Client{Timeout: maxHTTPTimeout, Transport: publicTransport(net.DefaultResolver.LookupIPAddr, (&net.Dialer{Timeout: maxHTTPTimeout}).DialContext), CheckRedirect: noRedirect}
}
func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

type resolveFunc func(context.Context, string) ([]net.IPAddr, error)
type dialFunc func(context.Context, string, string) (net.Conn, error)

func publicTransport(resolve resolveFunc, dial dialFunc) *http.Transport {
	return &http.Transport{Proxy: nil, DisableKeepAlives: true, TLSHandshakeTimeout: maxHTTPTimeout, ResponseHeaderTimeout: maxHTTPTimeout, MaxResponseHeaderBytes: maxResponseBytes, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil {
			return nil, errf("callback")
		}
		var addrs []net.IPAddr
		if ip, e := netip.ParseAddr(host); e == nil {
			addrs = []net.IPAddr{{IP: ip.AsSlice(), Zone: ip.Zone()}}
		} else {
			addrs, e = resolve(ctx, host)
			if e != nil {
				return nil, errf("callback")
			}
		}
		if !publicAddrs(addrs) {
			return nil, errf("callback")
		}
		// Every answer must pass validation before any connection is attempted. Dial
		// literal addresses so DNS cannot be consulted again by the socket layer.
		for _, a := range addrs {
			conn, e := dial(ctx, network, net.JoinHostPort(a.IP.String(), port))
			if e == nil {
				return conn, nil
			}
			if ctx.Err() != nil {
				break
			}
		}
		return nil, errf("network")
	}}
}
func (d *Delivery) post(ctx context.Context, sub *subscriptionRecord, id string, body []byte) (*http.Response, error) {
	if len(body) > maxEventBodyBytes {
		return nil, errf("unknown")
	}
	if e := validateCallbackURL(sub.Params.Delivery.URL); e != nil {
		return nil, e
	}
	ctx, cancel := context.WithTimeout(ctx, maxHTTPTimeout)
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, sub.Params.Delivery.URL, bytes.NewReader(body))
	if e != nil {
		cancel()
		return nil, errf("callback")
	}
	stamp := time.Now().UTC().Unix()
	setWebhookHeaders(req, id, stamp, signPayload(sub, id, body, stamp), sub.ID)
	resp, e := d.httpClient().Do(req)
	if e != nil {
		var networkError net.Error
		timedOut := errors.Is(e, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) || (errors.As(e, &networkError) && networkError.Timeout())
		cancel()
		var callbackErr *Error
		if errors.As(e, &callbackErr) && callbackErr.Code == "callback" {
			return nil, errf("callback")
		}
		if timedOut {
			return nil, errf("timeout")
		}
		return nil, errf("network")
	}
	resp.Body = &cancelBody{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelBody) Close() error { defer b.cancel(); return b.ReadCloser.Close() }
func (d *Delivery) verify(ctx context.Context, sub *subscriptionRecord) error {
	challenge, e := randomToken(32)
	if e != nil {
		return e
	}
	id, e := randomToken(16)
	if e != nil {
		return e
	}
	resp, e := d.post(ctx, sub, id, mustMarshal(map[string]string{"type": "verification", "challenge": challenge}))
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errf("verification")
	}
	data, e := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	var reply struct {
		Challenge string `json:"challenge"`
	}
	if e != nil || len(data) > maxResponseBytes || json.Unmarshal(data, &reply) != nil || subtle.ConstantTimeCompare([]byte(reply.Challenge), []byte(challenge)) != 1 {
		return errf("verification")
	}
	return nil
}
func (d *Delivery) deliverBytes(ctx context.Context, sub *subscriptionRecord, id string, body []byte) (string, error) {
	resp, e := d.post(ctx, sub, id, body)
	if e != nil {
		return "network", errf("network")
	}
	defer resp.Body.Close()
	// Event response bodies have no protocol meaning. Do not read arbitrary error
	// pages; closing the non-reused connection bounds both memory and work.
	code := strconv.Itoa(resp.StatusCode)
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return code, nil
	case resp.StatusCode == 410 || resp.StatusCode == 413:
		return code, errf("terminal")
	case resp.StatusCode == 429:
		return code, errf("rate_limited")
	case resp.StatusCode >= 500:
		return code, errf("server")
	default:
		return code, errf("client")
	}
}
func eventBodyBytes(n Notice) []byte {
	// A top-level "type" identifies only protocol control notifications such as
	// the verification challenge. An event occurrence carries eventId/name/
	// timestamp/data and a null cursor.
	return mustMarshal(struct {
		EventID   string    `json:"eventId"`
		Name      string    `json:"name"`
		Timestamp time.Time `json:"timestamp"`
		Data      struct {
			QueueID string `json:"queue_id"`
			JobID   string `json:"job_id"`
			Mode    string `json:"mode"`
		} `json:"data"`
		Cursor any `json:"cursor"`
	}{EventID: n.EventID, Name: EventName, Timestamp: n.OccurredAt, Data: struct {
		QueueID string `json:"queue_id"`
		JobID   string `json:"job_id"`
		Mode    string `json:"mode"`
	}{n.QueueID, n.JobID, n.Mode}})
}
func setWebhookHeaders(req *http.Request, id string, stamp int64, sigs []string, subID string) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("webhook-id", id)
	req.Header.Set("webhook-timestamp", strconv.FormatInt(stamp, 10))
	req.Header.Set("webhook-signature", strings.Join(sigs, " "))
	req.Header.Set("X-MCP-Subscription-Id", subID)
}
func signPayload(sub *subscriptionRecord, id string, body []byte, stamp int64) []string {
	msg := id + "." + strconv.FormatInt(stamp, 10) + "." + string(body)
	out := []string{signature(sub.Secret, msg)}
	if sub.PreviousSecret != "" && time.Now().Before(sub.RotateUntil) {
		out = append(out, signature(sub.PreviousSecret, msg))
	}
	return out
}
func signature(secret, msg string) string {
	key, e := decodeSecret(secret)
	if e != nil {
		return ""
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(msg))
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, e := rand.Read(b); e != nil {
		return "", errf("unknown")
	}
	return hex.EncodeToString(b), nil
}
