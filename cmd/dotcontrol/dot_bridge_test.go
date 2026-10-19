package main

import (
	"context"
	"errors"
	"testing"

	"github.com/dotcontrol/dotcontrol/internal/config"
)

type unavailableBridgeVault struct{ writes int }

func (v *unavailableBridgeVault) Get(string) (string, error) {
	return "", errors.New("vault unavailable")
}
func (v *unavailableBridgeVault) Set(string, string) error { v.writes++; return nil }
func TestOptionalBridgeUnavailableDoesNotReplaceKey(t *testing.T) {
	v := &unavailableBridgeVault{}
	s, e := openRuntimeDotBridge(context.Background(), t.TempDir(), config.Config{Listen: "127.0.0.1:9317"}, v, nil, nil)
	if e == nil || s != nil || v.writes != 0 {
		t.Fatalf("service=%v err=%v writes=%d", s, e, v.writes)
	}
}
