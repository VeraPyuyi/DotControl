package dotqueue

import (
	"encoding/hex"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	maxMessages     = 32
	maxPromptBytes  = 256 * 1024
	maxResultBytes  = 256 * 1024
	maxSummaryBytes = 2048
	maxArtifacts    = 16
	maxLabelBytes   = 256
	maxRetainedJobs = 100
	maxIDLen        = 128
	maxURLBytes     = 2048
)

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var hostPattern = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9.-]*[a-zA-Z0-9])?$`)
var allowedRoles = map[string]bool{"user": true, "assistant": true, "system": true}

func validateID(id string) error {
	if !idPattern.MatchString(id) {
		return errf("unknown")
	}
	return nil
}
func validHex(s string) bool {
	if len(s) != 64 || strings.ToLower(s) != s {
		return false
	}
	_, e := hex.DecodeString(s)
	return e == nil
}
func validateSubmission(s Submission) error {
	if validateID(s.RequestID) != nil || (s.SessionID != "" && validateID(s.SessionID) != nil) || (s.Mode != ModeAnswer && s.Mode != ModeTask) || len(s.Messages) > maxMessages || !utf8.ValidString(s.Task) {
		return errf("unknown")
	}
	if s.Mode == ModeAnswer && len(s.Messages) == 0 || s.Mode == ModeTask && strings.TrimSpace(s.Task) == "" {
		return errf("unknown")
	}
	n := len(s.Task)
	for _, m := range s.Messages {
		if !allowedRoles[m.Role] || !utf8.ValidString(m.Content) {
			return errf("unknown")
		}
		n += len(m.Content)
	}
	if n > maxPromptBytes {
		return errf("unknown")
	}
	return nil
}
func validateProgressState(s string) error {
	switch s {
	case StateClaimed, StateRunning, StateWaitingUser:
		return nil
	}
	return errf("unknown")
}
func validateSummary(s string) error {
	if len(s) > maxSummaryBytes || !utf8.ValidString(s) {
		return errf("unknown")
	}
	return nil
}
func validateResult(r Result) error {
	if !utf8.ValidString(r.Text) || len(r.Artifacts) > maxArtifacts || r.ErrorCode != "" && validateID(r.ErrorCode) != nil || r.Cancelled && r.ErrorCode != "" {
		return errf("unknown")
	}
	n := len(r.Text) + len(r.ErrorCode)
	for _, a := range r.Artifacts {
		if validateArtifact(a) != nil {
			return errf("unknown")
		}
		n += len(a.URL) + len(a.Label)
	}
	if n > maxResultBytes {
		return errf("unknown")
	}
	return nil
}
func validateArtifact(a Artifact) error {
	if len(a.Label) > maxLabelBytes || !utf8.ValidString(a.Label) || len(a.URL) > maxURLBytes || !utf8.ValidString(a.URL) || strings.Contains(a.URL, "\\") {
		return errf("unknown")
	}
	u, e := url.Parse(a.URL)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Opaque != "" {
		return errf("unknown")
	}
	if p := u.Port(); p != "" {
		n, e := strconv.Atoi(p)
		if e != nil || n < 1 || n > 65535 {
			return errf("unknown")
		}
	}
	h := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if ip, e := netip.ParseAddr(h); e == nil {
		ip = ip.Unmap()
		if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.Zone() != "" || netip.MustParsePrefix("100.64.0.0/10").Contains(ip) {
			return errf("unknown")
		}
	} else {
		// Reject browser-style abbreviated/numeric IPv4 aliases as hostnames.
		numeric := true
		for _, c := range h {
			if c != '.' && (c < '0' || c > '9') {
				numeric = false
			}
		}
		if numeric {
			return errf("unknown")
		}
		if !hostPattern.MatchString(h) || !strings.Contains(h, ".") || strings.HasSuffix(h, ".localhost") || h == "localhost" || strings.HasSuffix(h, ".local") || strings.Contains(h, "..") {
			return errf("unknown")
		}
		for _, label := range strings.Split(h, ".") {
			if len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
				return errf("unknown")
			}
		}
	}
	return nil
}
