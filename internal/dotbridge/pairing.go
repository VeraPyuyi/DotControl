package dotbridge

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/dotcontrol/dotcontrol/internal/vault"
)

var pairingID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var errPrivateBridgeFile = errors.New("bridge_private_file_invalid")

// The parent tree must be trusted local user/OS directories. Reject links in
// every component; this does not isolate against a malicious same-user process.
func pairingDirectory(root string, create bool) error {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || strings.HasPrefix(filepath.VolumeName(root), `\\`) || strings.ContainsFunc(root, unicode.IsControl) || root == filepath.VolumeName(root)+string(filepath.Separator) || strings.Contains(strings.TrimPrefix(root, filepath.VolumeName(root)), ":") {
		return errPrivateBridgeFile
	}
	for p := root; ; p = filepath.Dir(p) {
		i, e := os.Lstat(p)
		if e == nil {
			if !i.IsDir() || pairingRejectLink(i) != nil {
				return errPrivateBridgeFile
			}
		} else if !os.IsNotExist(e) {
			return e
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	if create {
		if e := os.MkdirAll(root, 0700); e != nil {
			return e
		}
	}
	i, e := os.Lstat(root)
	if e != nil {
		return e
	}
	if !i.IsDir() || pairingRejectLink(i) != nil {
		return errPrivateBridgeFile
	}
	return pairingPrivatePath(root, true)
}

func pairingCheckFile(path string, missingOK bool) error {
	i, e := os.Lstat(path)
	if missingOK && os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if !i.Mode().IsRegular() || pairingRejectLink(i) != nil {
		return errPrivateBridgeFile
	}
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	actual, e := f.Stat()
	if e != nil {
		return e
	}
	if !os.SameFile(i, actual) || pairingRejectHardlink(f, actual) != nil {
		return errPrivateBridgeFile
	}
	return nil
}

func writePrivateBridgeFile(root, name string, data []byte) error {
	if e := pairingDirectory(root, true); e != nil {
		return e
	}
	path := filepath.Join(root, name)
	if e := pairingCheckFile(path, true); e != nil {
		return e
	}
	f, e := os.CreateTemp(root, ".bridge-private-*")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = pairingPrivatePath(tmp, false); e != nil {
		f.Close()
		return e
	}
	n, we := f.Write(data)
	se := f.Sync()
	ce := f.Close()
	if we != nil {
		return we
	}
	if se != nil {
		return se
	}
	if ce != nil {
		return ce
	}
	if n != len(data) {
		return io.ErrShortWrite
	}
	if e = pairingCheckFile(path, true); e != nil {
		return e
	}
	return pairingReplace(tmp, path)
}

func readPrivateBridgeFile(root, name string) ([]byte, error) {
	if e := pairingDirectory(root, false); e != nil {
		return nil, e
	}
	path := filepath.Join(root, name)
	if e := pairingCheckFile(path, false); e != nil {
		return nil, e
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	actual, e := f.Stat()
	if e != nil {
		return nil, e
	}
	current, e := os.Lstat(path)
	if e != nil {
		return nil, e
	}
	if !actual.Mode().IsRegular() || pairingRejectLink(current) != nil || !os.SameFile(actual, current) || pairingRejectHardlink(f, actual) != nil || actual.Size() > 1<<20 {
		return nil, errPrivateBridgeFile
	}
	if e = pairingPrivatePath(path, false); e != nil {
		return nil, e
	}
	return io.ReadAll(io.LimitReader(f, 1<<20))
}

// WritePairingFile writes a pairing credential to a private local file.
func WritePairingFile(root string, file PairingFile) (string, error) {
	return writePairingFile(root, file)
}

func writePairingFile(root string, file PairingFile) (string, error) {
	if !pairingID.MatchString(file.ClientID) {
		return "", errors.New("invalid_client_id")
	}
	name := "pairing-" + file.ClientID + ".json"
	data, e := json.MarshalIndent(file, "", "  ")
	if e != nil {
		return "", e
	}
	if e = writePrivateBridgeFile(root, name, data); e != nil {
		return "", e
	}
	return filepath.Join(root, name), nil
}

// ReadPairingFile validates the filename and private local file before loading a credential.
func ReadPairingFile(root, clientID string) (PairingFile, error) {
	var file PairingFile
	if !pairingID.MatchString(clientID) {
		return file, errors.New("invalid_client_id")
	}
	data, e := readPrivateBridgeFile(root, "pairing-"+clientID+".json")
	if e != nil {
		return file, e
	}
	if json.Unmarshal(data, &file) != nil || file.ClientID != clientID || file.Token == "" || file.Version != APIVersion {
		return PairingFile{}, errors.New("pairing_file_invalid")
	}
	return file, nil
}

// Controller integration: replace service loadState/saveState raw file I/O with
// these helpers. Missing state retains os.IsNotExist semantics.
func securelyReadBridgeState(root string) ([]byte, error) {
	return readPrivateBridgeFile(root, "bridge.state.json")
}
func securelyWriteBridgeState(root string, data []byte) error {
	return writePrivateBridgeFile(root, "bridge.state.json", data)
}

func EnsureStateKey(v vault.Store) error {
	return vault.Ensure(v, StateKeyName, func() string { key, _ := randomStateKey(); return key })
}
