package dotevents

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

const (
	stateVersion  = 1
	markerName    = "dot_events.initialized"
	lockName      = "dot_events.lock"
	stateFileName = "events.state.de"
	maxStateBytes = 8 * 1024 * 1024
)

type subscriptionRecord struct {
	Generation       string    `json:"generation"`
	ID               string    `json:"id"`
	Principal        string    `json:"principal"`
	Params           Params    `json:"params"`
	Secret           string    `json:"secret"`
	PreviousSecret   string    `json:"previous_secret,omitempty"`
	RotateUntil      time.Time `json:"rotate_until,omitempty"`
	ExpiresAt        time.Time `json:"expires_at"`
	CallbackVerified bool      `json:"callback_verified"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type deliveryRecord struct {
	Body           []byte    `json:"body"`
	InFlightUntil  time.Time `json:"in_flight_until"`
	NextAttempt    time.Time `json:"next_attempt"`
	Terminal       bool      `json:"terminal"`
	SubscriptionID string    `json:"subscription_id"`
	EventID        string    `json:"event_id"`
	Attempts       int       `json:"attempts"`
	Acknowledged   bool      `json:"acknowledged"`
	LastCode       string    `json:"last_code,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type queueStatus struct {
	Attempts     int    `json:"attempts"`
	Acknowledged bool   `json:"acknowledged"`
	LastCode     string `json:"last_code,omitempty"`
	LastEventID  string `json:"last_event_id,omitempty"`
}

type persistenceState struct {
	StateVersion  int                            `json:"state_version"`
	Enabled       bool                           `json:"enabled"`
	Subscriptions map[string]*subscriptionRecord `json:"subscriptions"`
	Deliveries    map[string]*deliveryRecord     `json:"deliveries"`
	QueueStatus   map[string]*queueStatus        `json:"queue_status"`
}

func newPersistenceState() *persistenceState {
	return &persistenceState{
		StateVersion:  stateVersion,
		Enabled:       true,
		Subscriptions: map[string]*subscriptionRecord{},
		Deliveries:    map[string]*deliveryRecord{},
		QueueStatus:   map[string]*queueStatus{},
	}
}

func parsePersistenceState(data []byte) (*persistenceState, error) {
	if len(data) > maxStateBytes || !json.Valid(data) {
		return nil, errf("corrupt")
	}
	var st persistenceState
	if json.Unmarshal(data, &st) != nil {
		return nil, errf("corrupt")
	}
	if st.StateVersion != stateVersion || st.Subscriptions == nil || st.Deliveries == nil || st.QueueStatus == nil || len(st.Subscriptions) > maxActiveSubs || len(st.Deliveries) > maxDeliveryRecords || len(st.QueueStatus) > maxDeliveryRecords {
		return nil, errf("corrupt")
	}
	for id, sub := range st.Subscriptions {
		if sub == nil || sub.ID != id || sub.ID != subscriptionID(sub.Principal, sub.Params) || validateID(sub.Principal) != nil || validateParams(sub.Principal, sub.Params) != nil || validateSecret(sub.Secret) != nil || sub.Secret != sub.Params.Delivery.Secret || (sub.PreviousSecret != "" && validateSecret(sub.PreviousSecret) != nil) {
			return nil, errf("corrupt")
		}
	}
	for key, rec := range st.Deliveries {
		if rec == nil || deliveryKey(rec.SubscriptionID, rec.EventID) != key || validateID(rec.SubscriptionID) != nil || validateID(rec.EventID) != nil || rec.Attempts < 1 || rec.Attempts > 4 || len(rec.Body) > maxEventBodyBytes || !json.Valid(rec.Body) {
			return nil, errf("corrupt")
		}
	}
	for key, stRec := range st.QueueStatus {
		parts := splitQueueKey(key)
		if validateID(parts[0]) != nil || validateID(parts[1]) != nil {
			return nil, errf("corrupt")
		}
		if stRec == nil {
			return nil, errf("corrupt")
		}
	}
	return &st, nil
}

func subscriptionID(principal string, p Params) string {
	canon := struct {
		Principal string    `json:"principal"`
		Name      string    `json:"name"`
		Arguments Arguments `json:"arguments"`
		Delivery  struct {
			Mode string `json:"mode"`
			URL  string `json:"url"`
		} `json:"delivery"`
	}{
		Principal: principal,
		Name:      p.Name,
		Arguments: p.Arguments,
		Delivery: struct {
			Mode string `json:"mode"`
			URL  string `json:"url"`
		}{Mode: p.Delivery.Mode, URL: p.Delivery.URL},
	}
	sum := sha256.Sum256(mustMarshal(canon))
	return hex.EncodeToString(sum[:])
}

func deliveryKey(subscriptionID, eventID string) string {
	return subscriptionID + "\x00" + eventID
}

func queueKey(principal, queueID string) string {
	return principal + "\x00" + queueID
}

func splitQueueKey(key string) [2]string {
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

func activeSubscriptions(st *persistenceState, now time.Time) int {
	n := 0
	for _, sub := range st.Subscriptions {
		if sub != nil && sub.ExpiresAt.After(now) {
			n++
		}
	}
	return n
}

func pruneDeliveries(st *persistenceState) {
	if len(st.Deliveries) <= maxDeliveryRecords {
		return
	}
	var oldest string
	var oldestAt time.Time
	for key, rec := range st.Deliveries {
		if oldest == "" || rec.UpdatedAt.Before(oldestAt) {
			oldest = key
			oldestAt = rec.UpdatedAt
		}
	}
	if oldest != "" {
		delete(st.Deliveries, oldest)
	}
}
