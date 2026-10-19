package dotbridge

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/dotcontrol/dotcontrol/internal/vault"
)

func writePairingFile(root string, file PairingFile) (string, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	name := "pairing-" + file.ClientID + ".json"
	path := filepath.Join(root, name)
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return "", err
	}
	return path, nil
}

func EnsureStateKey(v vault.Store) error {
	return vault.Ensure(v, StateKeyName, func() string { key, _ := randomStateKey(); return key })
}
