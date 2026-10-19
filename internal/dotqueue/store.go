package dotqueue

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"
)

type Store struct {
	dir string
	key []byte
}

func Open(dir string, key []byte) (*Store, error) {
	if len(key) != 32 {
		return nil, errf("unknown")
	}
	if !localDir(dir) || checkAncestors(dir) != nil {
		return nil, errf("unknown")
	}
	return &Store{dir: dir, key: append([]byte(nil), key...)}, nil
}

func (s *Store) Initialize(enabled bool) error {
	return s.withLock(true, true, func(st *persistenceState) error {
		if st.Jobs == nil {
			*st = *newPersistenceState(enabled)
			return s.ensureMarker()
		}
		st.Enabled = enabled
		return nil
	})
}

func (s *Store) SetEnabled(enabled bool) error {
	return s.withLock(false, true, func(st *persistenceState) error {
		st.Enabled = enabled
		return nil
	})
}

func (s *Store) Submit(clientID string, sub Submission) (Job, error) {
	if err := validateID(clientID); err != nil {
		return Job{}, err
	}
	if err := validateSubmission(sub); err != nil {
		return Job{}, err
	}
	digest, err := submissionDigest(sub)
	if err != nil {
		return Job{}, err
	}
	var out Job
	err = s.withLock(false, true, func(st *persistenceState) error {
		if !st.Enabled {
			return errf("disabled")
		}
		key := requestKey(clientID, sub.RequestID)
		if existingID, ok := st.RequestIndex[key]; ok {
			existing := st.Jobs[existingID]
			if existing.RequestDigest != digest {
				return errf("conflict")
			}
			out = publicJob(existing)
			return nil
		}
		if len(st.Jobs) >= maxRetainedJobs {
			return errf("conflict")
		}
		now := time.Now().UTC()
		jobID, err := newID("job")
		if err != nil {
			return err
		}
		eventID, err := newID("evt")
		if err != nil {
			return err
		}
		rec := &jobRecord{
			ID:            jobID,
			RequestID:     sub.RequestID,
			RequestDigest: digest,
			ClientID:      clientID,
			QueueID:       clientID,
			Mode:          sub.Mode,
			State:         StateQueued,
			SessionID:     sub.SessionID,
			Task:          sub.Task,
			Messages:      append([]Message{}, sub.Messages...),
			CreatedAt:     now,
			UpdatedAt:     now,
			EventID:       eventID,
		}
		st.Jobs[jobID] = rec
		st.RequestIndex[key] = jobID
		st.Order = append(st.Order, jobID)
		out = publicJob(rec)
		return nil
	})
	return out, err
}

func (s *Store) Get(clientID, jobID string) (Job, error) {
	if err := validateID(clientID); err != nil {
		return Job{}, err
	}
	if err := validateID(jobID); err != nil {
		return Job{}, err
	}
	var out Job
	err := s.withLock(false, false, func(st *persistenceState) error {
		if !st.Enabled {
			return errf("disabled")
		}
		rec, err := s.jobForClient(st, clientID, jobID)
		if err != nil {
			return err
		}
		out = publicJob(rec)
		return nil
	})
	return out, err
}

func (s *Store) ByRequest(clientID, requestID string) (Job, error) {
	if err := validateID(clientID); err != nil {
		return Job{}, err
	}
	if err := validateID(requestID); err != nil {
		return Job{}, err
	}
	var out Job
	err := s.withLock(false, false, func(st *persistenceState) error {
		if !st.Enabled {
			return errf("disabled")
		}
		jobID, ok := st.RequestIndex[requestKey(clientID, requestID)]
		if !ok {
			return errf("notfound")
		}
		rec, err := s.jobForClient(st, clientID, jobID)
		if err != nil {
			return err
		}
		out = publicJob(rec)
		return nil
	})
	return out, err
}

func (s *Store) Claim(queueID, jobID, workerID string) (Claim, error) {
	if err := validateID(queueID); err != nil {
		return Claim{}, err
	}
	if err := validateID(jobID); err != nil {
		return Claim{}, err
	}
	if err := validateID(workerID); err != nil {
		return Claim{}, err
	}
	var out Claim
	err := s.withLock(false, true, func(st *persistenceState) error {
		if !st.Enabled {
			return errf("disabled")
		}
		rec := st.Jobs[jobID]
		if rec == nil || rec.QueueID != queueID {
			return errf("notfound")
		}
		if terminalState(rec.State) {
			return errf("conflict")
		}
		if rec.State == StateQueued {
			token, err := newClaimToken()
			if err != nil {
				return err
			}
			rec.State = StateClaimed
			rec.WorkerID = workerID
			rec.ClaimToken = token
			rec.UpdatedAt = time.Now().UTC()
		} else if rec.State == StateClaimed || rec.State == StateRunning || rec.State == StateWaitingUser || rec.State == StateCancelRequested {
			if rec.WorkerID != workerID {
				return errf("claim_conflict")
			}
			if rec.ClaimToken == "" {
				return errf("corrupt")
			}
		} else {
			return errf("conflict")
		}
		out = Claim{
			Job:        publicJob(rec),
			Submission: submissionFromRecord(rec),
			Token:      rec.ClaimToken,
		}
		return nil
	})
	return out, err
}

func (s *Store) Progress(jobID, token, state, summary string) (Job, error) {
	if err := validateID(jobID); err != nil {
		return Job{}, err
	}
	if err := validateProgressState(state); err != nil {
		return Job{}, err
	}
	if err := validateSummary(summary); err != nil {
		return Job{}, err
	}
	var out Job
	err := s.withLock(false, true, func(st *persistenceState) error {
		if !st.Enabled {
			return errf("disabled")
		}
		rec := st.Jobs[jobID]
		if rec == nil {
			return errf("notfound")
		}
		if !validHex(token) || !validHex(rec.ClaimToken) || rec.ClaimToken != token {
			return errf("conflict")
		}
		switch rec.State {
		case StateClaimed, StateRunning, StateWaitingUser, StateCancelRequested:
			rec.State = state
			rec.Summary = summary
			if rec.CancelRequested {
				rec.State = StateCancelRequested
			}
			rec.UpdatedAt = time.Now().UTC()
			out = publicJob(rec)
			return nil
		default:
			return errf("conflict")
		}
	})
	return out, err
}

func (s *Store) Complete(jobID, token string, result Result) (Job, error) {
	if err := validateID(jobID); err != nil {
		return Job{}, err
	}
	if err := validateResult(result); err != nil {
		return Job{}, err
	}
	digest, err := resultDigest(result)
	if err != nil {
		return Job{}, err
	}
	var out Job
	err = s.withLock(false, true, func(st *persistenceState) error {
		if !st.Enabled {
			return errf("disabled")
		}
		rec := st.Jobs[jobID]
		if rec == nil {
			return errf("notfound")
		}
		if !validHex(token) || !validHex(rec.ClaimToken) || rec.ClaimToken != token {
			return errf("conflict")
		}
		if terminalState(rec.State) && rec.State != StateOutcomeUnknown {
			if rec.ResultDigest == digest {
				out = publicJob(rec)
				return nil
			}
			return errf("conflict")
		}
		switch rec.State {
		case StateClaimed, StateRunning, StateWaitingUser, StateCancelRequested, StateOutcomeUnknown:
		default:
			return errf("conflict")
		}
		if result.Cancelled {
			if !rec.CancelRequested {
				return errf("conflict")
			}
			rec.State = StateCancelled
		} else if result.ErrorCode != "" {
			rec.State = StateFailed
			rec.ErrorCode = result.ErrorCode
		} else {
			rec.State = StateCompleted
		}
		rec.Result = &Result{
			Text:      result.Text,
			Artifacts: append([]Artifact{}, result.Artifacts...),
			ErrorCode: result.ErrorCode,
			Cancelled: result.Cancelled,
		}
		rec.ResultDigest = digest
		rec.UpdatedAt = time.Now().UTC()
		out = publicJob(rec)
		return nil
	})
	return out, err
}

func (s *Store) Cancel(clientID, jobID string) (Job, error) {
	if err := validateID(clientID); err != nil {
		return Job{}, err
	}
	if err := validateID(jobID); err != nil {
		return Job{}, err
	}
	var out Job
	err := s.withLock(false, true, func(st *persistenceState) error {
		if !st.Enabled {
			return errf("disabled")
		}
		rec, err := s.jobForClient(st, clientID, jobID)
		if err != nil {
			return err
		}
		if terminalState(rec.State) && rec.State != StateOutcomeUnknown {
			out = publicJob(rec)
			return nil
		}
		rec.CancelRequested = true
		rec.UpdatedAt = time.Now().UTC()
		switch rec.State {
		case StateQueued:
			rec.State = StateCancelled
		case StateOutcomeUnknown:
			// Preserve uncertainty while recording new cancellation intent.
		default:
			rec.State = StateCancelRequested
		}
		out = publicJob(rec)
		return nil
	})
	return out, err
}

func (s *Store) Recover() error {
	return s.withLock(false, true, func(st *persistenceState) error {
		if !st.Enabled {
			return errf("disabled")
		}
		now := time.Now().UTC()
		for _, id := range st.Order {
			rec := st.Jobs[id]
			if rec == nil || rec.Recovered {
				continue
			}
			switch rec.State {
			case StateClaimed, StateRunning, StateWaitingUser, StateCancelRequested:
				rec.State = StateOutcomeUnknown
				rec.Recovered = true
				rec.UpdatedAt = now
			}
		}
		return nil
	})
}

func (s *Store) Pending(queueID string) ([]Notice, error) {
	if err := validateID(queueID); err != nil {
		return nil, err
	}
	var out []Notice
	err := s.withLock(false, false, func(st *persistenceState) error {
		if !st.Enabled {
			return errf("disabled")
		}
		for _, id := range st.Order {
			rec := st.Jobs[id]
			if rec == nil || rec.QueueID != queueID || rec.State != StateQueued {
				continue
			}
			out = append(out, Notice{
				EventID:    rec.EventID,
				QueueID:    rec.QueueID,
				JobID:      rec.ID,
				Mode:       rec.Mode,
				OccurredAt: rec.CreatedAt,
			})
		}
		return nil
	})
	return out, err
}

func (s *Store) jobForClient(st *persistenceState, clientID, jobID string) (*jobRecord, error) {
	rec := st.Jobs[jobID]
	if rec == nil || rec.ClientID != clientID {
		return nil, errf("notfound")
	}
	return rec, nil
}

func (s *Store) withLock(allowCreate bool, mutate bool, fn func(*persistenceState) error) error {
	if err := s.checkDir(allowCreate); err != nil {
		return err
	}
	lockPath := filepath.Join(s.dir, lockName)
	if err := regularFile(lockPath); err != nil {
		return wrapUnknown(err)
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return errf("unknown")
	}
	info, statErr := f.Stat()
	if statErr != nil || !info.Mode().IsRegular() || rejectHardlink(f, info) != nil {
		f.Close()
		return errf("unknown")
	}
	defer f.Close()
	if privatePath(lockPath, false) != nil {
		return errf("unknown")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		ok, e := tryHandleLock(f)
		if e != nil {
			return errf("unknown")
		}
		if ok {
			break
		}
		if time.Now().After(deadline) {
			return errf("unknown")
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer unlockHandle(f)
	// Verify the locked handle still names the checked private regular file.
	current, e := os.Lstat(lockPath)
	if e != nil || rejectLink(current) != nil || !os.SameFile(info, current) || rejectHardlink(f, info) != nil {
		return errf("unknown")
	}

	st, err := s.loadState(allowCreate)
	if err != nil {
		return err
	}
	if st == nil {
		var empty persistenceState
		if err := fn(&empty); err != nil {
			return err
		}
		return s.saveState(&empty)
	}
	if mutate {
		if err := fn(st); err != nil {
			return err
		}
		return s.saveState(st)
	}
	return fn(st)
}

func (s *Store) loadState(allowCreate bool) (*persistenceState, error) {
	marker := filepath.Join(s.dir, markerName)
	_, markerErr := os.Lstat(marker)
	statePath := filepath.Join(s.dir, stateFileName)
	_, stateErr := os.Lstat(statePath)
	if os.IsNotExist(markerErr) && os.IsNotExist(stateErr) {
		if allowCreate {
			return nil, nil
		}
		return nil, errf("corrupt")
	}
	if markerErr != nil || stateErr != nil {
		if os.IsNotExist(stateErr) && markerErr == nil {
			return nil, errf("corrupt")
		}
		return nil, errf("corrupt")
	}
	markerData, err := s.readPrivate(marker, 16)
	if err != nil || string(markerData) != "1\n" {
		return nil, errf("corrupt")
	}
	if err := regularFile(statePath); err != nil {
		return nil, wrapUnknown(err)
	}
	f, err := os.Open(statePath)
	if err != nil {
		return nil, errf("corrupt")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || rejectHardlink(f, info) != nil {
		return nil, errf("corrupt")
	}
	if privatePath(statePath, false) != nil {
		return nil, errf("unknown")
	}
	if info.Size() > maxStateBytes {
		return nil, errf("corrupt")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxStateBytes+1))
	if err != nil {
		return nil, errf("corrupt")
	}
	plain, err := openEnvelope(s.key, data)
	if err != nil {
		return nil, err
	}
	return parsePersistenceState(plain)
}

func (s *Store) saveState(st *persistenceState) error {
	if st.StateVersion != stateVersion {
		return errf("corrupt")
	}
	data, err := json.Marshal(st)
	if err != nil || len(data) > maxStateBytes {
		return errf("unknown")
	}
	sealed, err := seal(s.key, data)
	if err != nil {
		return err
	}
	statePath := filepath.Join(s.dir, stateFileName)
	if err := regularFile(statePath); err != nil {
		return wrapUnknown(err)
	}
	f, err := os.CreateTemp(s.dir, ".dot-queue-*")
	if err != nil {
		return errf("unknown")
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if privatePath(tmp, false) != nil {
		f.Close()
		return errf("unknown")
	}
	n, writeErr := f.Write(sealed)
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil || n != len(sealed) || syncErr != nil || closeErr != nil {
		return errf("unknown")
	}
	if regularFile(statePath) != nil || replaceFile(tmp, statePath) != nil || syncDirectory(s.dir) != nil {
		return errf("unknown")
	}
	return nil
}

func (s *Store) ensureMarker() error {
	path := filepath.Join(s.dir, markerName)
	if err := regularFile(path); err != nil {
		return wrapUnknown(err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(err) {
		f, err = os.Open(path)
		if err != nil {
			return errf("unknown")
		}
		info, statErr := f.Stat()
		if statErr != nil || !info.Mode().IsRegular() || rejectHardlink(f, info) != nil {
			f.Close()
			return errf("unknown")
		}
		if f.Close() != nil || privatePath(path, false) != nil {
			return errf("unknown")
		}
		return nil
	}
	if err != nil {
		return errf("unknown")
	}
	if privatePath(path, false) != nil {
		f.Close()
		return errf("unknown")
	}
	_, writeErr := f.WriteString("1\n")
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil || syncDirectory(s.dir) != nil {
		return errf("unknown")
	}
	return nil
}

func (s *Store) checkDir(create bool) error {
	if !localDir(s.dir) || checkAncestors(s.dir) != nil {
		return errf("unknown")
	}
	if create {
		if os.MkdirAll(s.dir, 0700) != nil {
			return errf("unknown")
		}
	}
	info, err := os.Lstat(s.dir)
	if os.IsNotExist(err) {
		return errf("corrupt")
	}
	if err != nil || !info.IsDir() || rejectLink(info) != nil {
		return errf("unknown")
	}
	if privatePath(s.dir, true) != nil {
		return errf("unknown")
	}
	return nil
}

func regularFile(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || rejectLink(info) != nil {
		return errf("unknown")
	}
	return nil
}

func newID(prefix string) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", errf("unknown")
	}
	return prefix + "." + hex.EncodeToString(b[:]), nil
}

func newClaimToken() (string, error) {
	var b [claimTokenBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", errf("unknown")
	}
	return hex.EncodeToString(b[:]), nil
}
