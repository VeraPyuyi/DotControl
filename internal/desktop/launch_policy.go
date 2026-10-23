package desktop

import (
	"errors"
	"strconv"
	"strings"
)

func launchField(data, key string) string {
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, key+" = ") {
			return strings.Trim(strings.TrimPrefix(line, key+" = "), `"`)
		}
	}
	return ""
}

func (s StartupSnapshot) launchRunning() bool {
	pid, err := strconv.Atoi(launchField(s.LoadedText, "pid"))
	return s.Loaded && err == nil && pid > 0
}

func (s StartupSnapshot) launchStartAction(exe, state string) (string, error) {
	if !s.BelongsToState(state) {
		return "", errors.New("managed_startup_unavailable")
	}
	if !s.matchesExecutable(exe) {
		return "", errors.New("conflicting_definition_paths")
	}
	if s.Disabled {
		return "", errors.New("managed_startup_disabled")
	}
	if s.Loaded {
		if s.launchRunning() {
			return "", nil
		}
		return "kickstart", nil
	}
	return "bootstrap", nil
}
