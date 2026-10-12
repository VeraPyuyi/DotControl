package connections

import (
	"strconv"
	"strings"
)

// Process inventories are larger than ordinary command diagnostics. Keep a
// separate finite limit and reject incomplete scans before parsing any rows.
const processMetadataLimit = 1024 * 1024

func parseProcessMetadata(result CommandResult) ([]processInfo, error) {
	if result.StdoutTruncated || result.StderrTruncated {
		return nil, failure("process_metadata_truncated")
	}
	if result.ExitCode != 0 {
		return nil, failure("process_metadata_unavailable")
	}
	var processes []processInfo
	for _, line := range strings.Split(result.Stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, e1 := strconv.ParseUint(fields[0], 10, 32)
		parent, e2 := strconv.ParseUint(fields[1], 10, 32)
		if e1 != nil || e2 != nil {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), fields[0]))
		path := strings.TrimSpace(strings.TrimPrefix(rest, fields[1]))
		processes = append(processes, processInfo{PID: uint32(pid), ParentPID: uint32(parent), Path: path})
	}
	return processes, nil
}
