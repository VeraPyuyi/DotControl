package vault

import (
	"errors"
	"github.com/zalando/go-keyring"
	"testing"
)

type fakeStore struct {
	value string
	err   error
	sets  int
}

func (v *fakeStore) Get(string) (string, error) { return v.value, v.err }
func (v *fakeStore) Set(_, value string) error  { v.value = value; v.sets++; return nil }
func TestRepairPreservesExistingOrUnavailableCredential(t *testing.T) {
	for _, v := range []*fakeStore{{value: "existing"}, {err: errors.New("keychain locked")}, {value: ""}} {
		_ = Ensure(v, "token", func() string { return "new" })
		if v.sets != 0 {
			t.Fatal("overwrote non-absent credential")
		}
	}
	missing := &fakeStore{err: keyring.ErrNotFound}
	if err := Ensure(missing, "token", func() string { return "new" }); err != nil || missing.sets != 1 {
		t.Fatal("missing initialization failed")
	}
}
