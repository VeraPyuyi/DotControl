// Package vault stores secrets in the Windows Credential Manager or macOS Keychain.
package vault

import (
	"crypto/sha256"
	"encoding/hex"
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
