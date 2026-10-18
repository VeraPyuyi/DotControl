package driver

import (
	"encoding/json"
	"errors"
	"os"
	"sync"
)

const NativeDiagnosticBuild = "alpha2-native-1"

var failureJournal struct {
	sync.Mutex
	file   *os.File
	failed bool
}

// OpenFailureJournal creates only a dedicated private diagnostic file. It never
// reads configuration or credentials, changes TCC grants, or alters signatures.
func OpenFailureJournal(path string) (func(), error) {
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return nil, errors.New("diagnostic_log_not_private_regular_file")
		}
	} else if !os.IsNotExist(err) {
		return nil, errors.New("diagnostic_log_unavailable")
	}
	file, err := openPrivateJournalFile(path)
	if err != nil {
		return nil, errors.New("diagnostic_log_unavailable")
	}
	if err := validatePrivateJournalFile(file); err != nil {
		file.Close()
		return nil, errors.New("diagnostic_log_not_private_regular_file")
	}
	failureJournal.Lock()
	if failureJournal.file != nil {
		failureJournal.Unlock()
		file.Close()
		return nil, errors.New("diagnostic_log_already_open")
	}
	failureJournal.file, failureJournal.failed = file, false
	failureJournal.Unlock()
	return func() {
		failureJournal.Lock()
		defer failureJournal.Unlock()
		if failureJournal.file == file {
			failureJournal.file = nil
			_ = file.Close()
		}
	}, nil
}

func persistFailure(failure callFailure) {
	failureJournal.Lock()
	defer failureJournal.Unlock()
	if failureJournal.file == nil {
		return
	}
	record := struct {
		Build string `json:"diagnostic_build"`
		callFailure
	}{NativeDiagnosticBuild, failure}
	if json.NewEncoder(failureJournal.file).Encode(record) != nil || failureJournal.file.Sync() != nil {
		failureJournal.failed = true
	}
}

func failureJournalStatus() (bool, bool) {
	failureJournal.Lock()
	defer failureJournal.Unlock()
	return failureJournal.file != nil, failureJournal.failed
}
