package codexbridge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"

	"github.com/dotcontrol/dotcontrol/internal/connections"
)

var terminalConfigHashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// TerminalConfigHash binds local enablement to the complete normalized saved
// connection configuration. It is not proof that upstream permission profile
// definitions or the executable's contents are immutable.
func TerminalConfigHash(cfg connections.Config) string {
	cfg.ExecutionProfiles = append([]connections.ExecutionProfile(nil), cfg.ExecutionProfiles...)
	cfg.Normalize()
	raw, _ := json.Marshal(cfg)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
