package dotevents

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	maxCallbackURLBytes = 2048
	maxParamsBytes      = 16 * 1024
	maxActiveSubs       = 100
	maxDeliveryRecords  = 1000
	minSecretBytes      = 24
	maxSecretBytes      = 64
	minTTL              = 60 * 1000
	maxTTL              = 24 * 60 * 60 * 1000
	defaultTTL          = 60 * 60 * 1000
)

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var hostPattern = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9.-]*[a-zA-Z0-9])?$`)

func validateID(id string) error {
	if !idPattern.MatchString(id) {
		return errf("unknown")
	}
	return nil
}

func validateParams(principal string, p Params) error {
	if err := validateID(principal); err != nil {
		return err
	}
	if p.Name != EventName {
		return errf("unknown")
	}
	if err := validateID(p.Arguments.QueueID); err != nil || p.Arguments.QueueID != principal {
		return errf("unknown")
	}
	if p.Delivery.Mode != "webhook" {
		return errf("unknown")
	}
	if err := validateSecret(p.Delivery.Secret); err != nil {
		return err
	}
	if err := validateCursor(p.Cursor); err != nil {
		return err
	}
	if _, err := parseTTL(p.TTL); err != nil {
		return err
	}
	if err := validateCallbackURL(p.Delivery.URL); err != nil {
		return err
	}
	if len(mustMarshal(p)) > maxParamsBytes {
		return errf("unknown")
	}
	return nil
}

func validateSecret(secret string) error {
	if !strings.HasPrefix(secret, "whsec_") {
		return errf("unknown")
	}
	raw, err := base64.StdEncoding.DecodeString(secret[len("whsec_"):])
	if err != nil || len(raw) < minSecretBytes || len(raw) > maxSecretBytes {
		return errf("unknown")
	}
	return nil
}

func decodeSecret(secret string) ([]byte, error) {
	if err := validateSecret(secret); err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(secret[len("whsec_"):])
}

func validateCursor(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	if !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errf("unknown")
	}
	return nil
}

func parseTTL(raw json.RawMessage) (int64, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return defaultTTL, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] < '0' || trimmed[0] > '9' {
		return 0, errf("unknown")
	}
	var n json.Number
	if json.Unmarshal(raw, &n) != nil {
		return 0, errf("unknown")
	}
	if strings.Contains(n.String(), ".") {
		return 0, errf("unknown")
	}
	v, err := n.Int64()
	if err != nil || v < minTTL || v > maxTTL {
		return 0, errf("unknown")
	}
	return v, nil
}

func validateCallbackURL(raw string) error {
	if callbackURLRule(raw) != "" {
		return errf("callback")
	}
	return nil
}

// callbackURLRule returns only a fixed policy category, never URL content.
func callbackURLRule(raw string) string {
	if len(raw) > maxCallbackURLBytes {
		return "url_length"
	}
	if !utf8.ValidString(raw) || strings.ContainsAny(raw, "\\#") {
		return "url_characters"
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "url_parse"
	}
	if u.Scheme != "https" {
		return "https_required"
	}
	if u.User != nil || u.Fragment != "" || u.Opaque != "" || u.Host == "" {
		return "url_structure"
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return "local_hostname"
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if !publicIP(ip) {
			return "nonpublic_address"
		}
		return ""
	}
	if !hostPattern.MatchString(host) || !strings.Contains(host, ".") || strings.Contains(host, "..") {
		return "hostname_structure"
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "hostname_label"
		}
	}
	return ""
}
func validCallbackRule(rule string) bool {
	switch rule {
	case "", "url_length", "url_characters", "url_parse", "https_required", "url_structure", "local_hostname", "nonpublic_address", "hostname_structure", "hostname_label":
		return true
	}
	return false
}

func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.Zone() != "" || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	if ip.Is4() {
		b := ip.As4()
		if b[0] == 0 || (b[0] == 192 && b[1] == 0 && b[2] == 0) || (b[0] == 192 && b[1] == 88 && b[2] == 99) || (b[0] == 198 && b[1] == 19) {
			return false
		}
		if b[0] == 100 && b[1] >= 64 && b[1] <= 127 {
			return false
		}
		if b[0] == 192 && b[1] == 0 && b[2] == 2 {
			return false
		}
		if b[0] == 198 && (b[1] == 51 || b[1] == 18) {
			return false
		}
		if b[0] == 203 && b[1] == 0 && b[2] == 113 {
			return false
		}
		if b[0] >= 240 {
			return false
		}
		return true
	}
	if ip.Is6() {
		b := ip.As16()
		// Only globally allocated unicast IPv6; exclude special-purpose,
		// transition mechanisms, benchmarking and documentation allocations.
		if b[0]&0xe0 != 0x20 || (b[0] == 0x20 && b[1] == 0x01 && b[2] < 2) || (b[0] == 0x20 && b[1] == 0x02) || (b[0] == 0x3f && b[1] == 0xff) {
			return false
		}
		if b[0] == 0x64 && b[1] == 0xff {
			return false
		}
		if b[0] == 0x20 && b[1] == 0x01 && b[2] == 0x0d && b[3] == 0xb8 {
			return false
		}
		if b[0] == 0xfd {
			return false
		}
		if b[0] == 0xfe && b[1] == 0x80 {
			return false
		}
	}
	return true
}

func publicAddrs(addrs []net.IPAddr) bool {
	if len(addrs) == 0 {
		return false
	}
	for _, addr := range addrs {
		ip, ok := netip.AddrFromSlice(addr.IP)
		if !ok || addr.Zone != "" || !publicIP(ip) {
			return false
		}
	}
	return true
}

func mustMarshal(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
