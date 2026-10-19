package dotbridge

import (
	"github.com/zalando/go-keyring"
	"sync"
)

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
	return "", keyring.ErrNotFound
}

func (f *FakeVault) Set(name, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data[name] = value
	return nil
}
