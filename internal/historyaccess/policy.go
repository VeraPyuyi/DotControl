// Package historyaccess stores explicit local history consent and resolves
// read-only sidebar metadata into candidates for separate human review.
package historyaccess

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
)

const maxThreadIDs = 10000

type Policy struct {
	Version          int      `json:"version"`
	Mode             string   `json:"mode"`
	Revision         uint64   `json:"revision"`
	AllowedThreadIDs []string `json:"allowed_thread_ids"`
}

var threadID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ValidateThreadID accepts native stable UUIDs, never paths or client aliases.
func ValidateThreadID(id string) bool { return threadID.MatchString(id) }

func failure(code string) error { return errors.New("history_" + code) }

func normalized(p Policy) (Policy, error) {
	if p.Version != 1 || p.Revision == 0 || (p.Mode != "all" && p.Mode != "allowlist") || len(p.AllowedThreadIDs) > maxThreadIDs || (p.Mode == "all" && len(p.AllowedThreadIDs) != 0) {
		return Policy{}, failure("invalid_policy")
	}
	for _, id := range p.AllowedThreadIDs {
		if !ValidateThreadID(id) {
			return Policy{}, failure("invalid_policy")
		}
	}
	p.AllowedThreadIDs = append([]string{}, p.AllowedThreadIDs...)
	slices.Sort(p.AllowedThreadIDs)
	p.AllowedThreadIDs = slices.Compact(p.AllowedThreadIDs)
	return p, nil
}

// Fingerprint binds the full normalized policy, including its revision. An
// invalid policy has no usable fingerprint.
func Fingerprint(p Policy) string {
	p, err := normalized(p)
	if err != nil {
		return ""
	}
	b, _ := json.Marshal(p)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Allows fails closed for invalid policies and invalid thread identities.
func Allows(p Policy, id string) bool {
	if !ValidateThreadID(id) {
		return false
	}
	p, err := normalized(p)
	if err != nil {
		return false
	}
	return p.Mode == "all" || slices.Contains(p.AllowedThreadIDs, id)
}
