package dotqueue

import (
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	maxMessages       = 32
	maxPromptBytes    = 256 * 1024
	maxResultBytes    = 256 * 1024
	maxSummaryBytes   = 2048
	maxArtifacts      = 16
	maxLabelBytes     = 256
	maxRetainedJobs   = 100
	maxIDLen          = 128
)

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

var allowedRoles = map[string]bool{
	"user":      true,
	"assistant": true,
	"system":    true,
}

func validateID(id string) error {
	if !idPattern.MatchString(id) {
		return errf("unknown")
	}
	return nil
}

func validateSubmission(sub Submission) error {
	if err := validateID(sub.RequestID); err != nil {
		return err
	}
	if sub.Mode != ModeAnswer && sub.Mode != ModeTask {
		return errf("unknown")
	}
	if sub.Mode == ModeAnswer && len(sub.Messages) == 0 {
		return errf("unknown")
	}
	if sub.Mode == ModeTask && strings.TrimSpace(sub.Task) == "" {
		return errf("unknown")
	}
	if len(sub.Messages) > maxMessages {
		return errf("unknown")
	}
	var promptBytes int
	for _, m := range sub.Messages {
		if !allowedRoles[m.Role] {
			return errf("unknown")
		}
		promptBytes += len(m.Content)
	}
	if promptBytes > maxPromptBytes {
		return errf("unknown")
	}
	if sub.SessionID != "" {
		if err := validateID(sub.SessionID); err != nil {
			return err
		}
	}
	return nil
}

func validateProgressState(state string) error {
	switch state {
	case StateRunning, StateWaitingUser:
		return nil
	default:
		return errf("unknown")
	}
}

func validateSummary(summary string) error {
	if len(summary) > maxSummaryBytes {
		return errf("unknown")
	}
	if !utf8.ValidString(summary) {
		return errf("unknown")
	}
	return nil
}

func validateResult(res Result) error {
	if len(res.Text) > maxResultBytes {
		return errf("unknown")
	}
	if !utf8.ValidString(res.Text) {
		return errf("unknown")
	}
	if len(res.Artifacts) > maxArtifacts {
		return errf("unknown")
	}
	for _, a := range res.Artifacts {
		if err := validateArtifact(a); err != nil {
			return err
		}
	}
	return nil
}

func validateArtifact(a Artifact) error {
	if len(a.Label) > maxLabelBytes || !utf8.ValidString(a.Label) {
		return errf("unknown")
	}
	u, err := url.Parse(a.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return errf("unknown")
	}
	if strings.Contains(a.URL, "\\") || strings.HasPrefix(a.URL, "file:") {
		return errf("unknown")
	}
	return nil
}
