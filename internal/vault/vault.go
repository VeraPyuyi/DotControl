// Package vault stores secrets in the Windows Credential Manager or macOS Keychain.
package vault

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/zalando/go-keyring"
	"path/filepath"
	"strings"
)

type Vault struct{ service string }

func New(dir string) Vault {
	abs, _ := filepath.Abs(dir)
	h := sha256.Sum256([]byte(strings.ToLower(abs)))
	return Vault{service: "DotControl/" + hex.EncodeToString(h[:12])}
}
func (v Vault) Get(name string) (string, error) { return keyring.Get(v.service, name) }
func (v Vault) Set(name, value string) error    { return keyring.Set(v.service, name, value) }
func (v Vault) Delete(name string) error        { return keyring.Delete(v.service, name) }

type Store interface {
	Get(string) (string, error)
	Set(string, string) error
}

// Ensure initializes only a definitely absent credential; an unavailable vault
// must never be mistaken for an empty vault during repair or upgrade.
func Ensure(v Store, name string, generate func() string) error {
	value, err := v.Get(name)
	if err == nil {
		if value == "" {
			return errors.New("credential_empty")
		}
		return nil
	}
	if !errors.Is(err, keyring.ErrNotFound) {
		return errors.New("credential_store_unavailable")
	}
	return v.Set(name, generate())
}
