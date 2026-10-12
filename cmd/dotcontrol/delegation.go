package main

import (
	"context"
	"errors"
	"github.com/dotcontrol/dotcontrol/internal/codexbridge"
	"github.com/dotcontrol/dotcontrol/internal/desktop"
	"regexp"
)

// This release table is an acceptance decision, not a user-editable bypass.
// Native desktop / remote approval stay off until their real host loop passes.
func delegationAcceptance() map[string]codexbridge.PlatformAcceptance {
	return map[string]codexbridge.PlatformAcceptance{}
}

var officialThreadID = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

// Public desktop deep link, documented at learn.chatgpt.com/docs/reference/commands.
// OS acceptance is an open request, not proof the app rendered the thread.
func openOwnedCodexThread(ctx context.Context, id string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !officialThreadID.MatchString(id) {
		return errors.New("invalid_official_thread_id")
	}
	return desktop.OpenURL("codex://threads/" + id)
}
