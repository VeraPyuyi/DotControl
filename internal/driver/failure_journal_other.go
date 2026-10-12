//go:build !darwin && !linux && !windows

package driver

import (
	"errors"
	"os"
)

func openPrivateJournalFile(path string) (*os.File, error) {
	return nil, errors.New("private_journal_unsupported")
}

func validatePrivateJournalFile(file *os.File) error {
	return errors.New("private_journal_unsupported")
}
