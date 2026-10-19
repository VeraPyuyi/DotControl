package dotbridge

import "sync"

type FakeVault struct {
	mu   sync.Mutex
	data map[string]string
}

func NewFakeVault() *FakeVault {
	return &FakeVault{data: map[string]string{}}
}

func (f *FakeVault) Get(name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v, ok := f.data[name]; ok {
		return v, nil
	}
	return "", errMissing
}

func (f *FakeVault) Set(name, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data[name] = value
	return nil
}

var errMissing = &vaultMissing{}

type vaultMissing struct{}

func (v *vaultMissing) Error() string { return "not_found" }
