package dotqueue

import (
	"encoding/json"
	"time"
)

const (
	stateVersion     = 1
	markerName       = "dot_queue.initialized"
	lockName         = "dot_queue.lock"
	stateFileName    = "queue.state.dq"
	maxStateBytes    = 64 * 1024 * 1024
	claimTokenBytes  = 32
)

type jobRecord struct {
	ID              string    `json:"id"`
	RequestID       string    `json:"request_id"`
	RequestDigest   string    `json:"request_digest"`
	ClientID        string    `json:"client_id"`
	QueueID         string    `json:"queue_id"`
	Mode            string    `json:"mode"`
	State           string    `json:"state"`
	SessionID       string    `json:"session_id"`
	Task            string    `json:"task"`
	Messages        []Message `json:"messages"`
	CancelRequested bool      `json:"cancel_requested"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	EventID         string    `json:"event_id"`
	WorkerID        string    `json:"worker_id"`
	ClaimToken      string    `json:"claim_token"`
	ResultDigest    string    `json:"result_digest"`
	Result          *Result   `json:"result,omitempty"`
	ErrorCode       string    `json:"error_code,omitempty"`
	Recovered       bool      `json:"recovered"`
}

type persistenceState struct {
	StateVersion int                    `json:"state_version"`
	Enabled      bool                   `json:"enabled"`
	Jobs         map[string]*jobRecord  `json:"jobs"`
	RequestIndex map[string]string      `json:"request_index"`
	Order        []string               `json:"order"`
}

func newPersistenceState(enabled bool) *persistenceState {
	return &persistenceState{
		StateVersion: stateVersion,
		Enabled:      enabled,
		Jobs:         map[string]*jobRecord{},
		RequestIndex: map[string]string{},
		Order:        []string{},
	}
}

func parsePersistenceState(data []byte) (*persistenceState, error) {
	obj, err := decodeObject(data, maxStateBytes)
	if err != nil {
		return nil, err
	}
	required := []string{"state_version", "enabled", "jobs", "request_index", "order"}
	for _, key := range required {
		if _, ok := obj[key]; !ok {
			return nil, errf("corrupt")
		}
	}
	for key := range obj {
		switch key {
		case "state_version", "enabled", "jobs", "request_index", "order":
		default:
			return nil, errf("corrupt")
		}
	}
	var st persistenceState
	if json.Unmarshal(data, &st) != nil {
		return nil, errf("corrupt")
	}
	if st.StateVersion != stateVersion {
		return nil, errf("corrupt")
	}
	if st.Jobs == nil || st.RequestIndex == nil || st.Order == nil {
		return nil, errf("corrupt")
	}
	for id, job := range st.Jobs {
		if job == nil || job.ID != id {
			return nil, errf("corrupt")
		}
		if err := validateStoredJob(job); err != nil {
			return nil, err
		}
	}
	for key, jobID := range st.RequestIndex {
		if st.Jobs[jobID] == nil {
			return nil, errf("corrupt")
		}
		parts := splitRequestKey(key)
		if parts[0] != st.Jobs[jobID].ClientID || parts[1] != st.Jobs[jobID].RequestID {
			return nil, errf("corrupt")
		}
	}
	if len(st.Order) != len(st.Jobs) {
		return nil, errf("corrupt")
	}
	seen := map[string]bool{}
	for _, id := range st.Order {
		if st.Jobs[id] == nil || seen[id] {
			return nil, errf("corrupt")
		}
		seen[id] = true
	}
	return &st, nil
}

func validateStoredJob(job *jobRecord) error {
	if err := validateID(job.ID); err != nil {
		return errf("corrupt")
	}
	if err := validateID(job.RequestID); err != nil {
		return errf("corrupt")
	}
	if err := validateID(job.ClientID); err != nil {
		return errf("corrupt")
	}
	if job.QueueID != job.ClientID {
		return errf("corrupt")
	}
	if err := validateID(job.EventID); err != nil {
		return errf("corrupt")
	}
	return nil
}

func requestKey(clientID, requestID string) string {
	return clientID + "\x00" + requestID
}

func splitRequestKey(key string) [2]string {
	parts := [2]string{"", ""}
	for i := 0; i < len(key); i++ {
		if key[i] == 0 {
			parts[0] = key[:i]
			parts[1] = key[i+1:]
			break
		}
	}
	return parts
}

func publicJob(rec *jobRecord) Job {
	return Job{
		ID:              rec.ID,
		RequestID:       rec.RequestID,
		RequestDigest:   rec.RequestDigest,
		ClientID:        rec.ClientID,
		QueueID:         rec.QueueID,
		Mode:            rec.Mode,
		State:           rec.State,
		SessionID:       rec.SessionID,
		CancelRequested: rec.CancelRequested,
		CreatedAt:       rec.CreatedAt,
		UpdatedAt:       rec.UpdatedAt,
		Result:          rec.Result,
		ErrorCode:       rec.ErrorCode,
	}
}

func submissionFromRecord(rec *jobRecord) Submission {
	return Submission{
		RequestID: rec.RequestID,
		Mode:      rec.Mode,
		SessionID: rec.SessionID,
		Messages:  append([]Message(nil), rec.Messages...),
		Task:      rec.Task,
	}
}

func terminalState(state string) bool {
	switch state {
	case StateCancelled, StateCompleted, StateFailed, StateOutcomeUnknown:
		return true
	default:
		return false
	}
}
