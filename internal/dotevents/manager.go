package dotevents

import (
	"context"
	"sort"
	"time"
)

type Manager struct {
	store    *store
	delivery *Delivery
}

func Open(dir string, key []byte, delivery *Delivery) (*Manager, error) {
	st, e := openStore(dir, key)
	if e != nil {
		return nil, e
	}
	if delivery == nil {
		delivery = &Delivery{}
	}
	m := &Manager{st, delivery}
	if e = st.withLock(true, true, func(*persistenceState) error { return nil }); e != nil {
		return nil, e
	}
	return m, nil
}
func (m *Manager) SetEnabled(enabled bool) error {
	return m.store.withLock(false, true, func(st *persistenceState) error {
		st.Enabled = enabled
		if !enabled {
			for _, s := range st.Subscriptions {
				s.CallbackVerified = false
				s.Generation = ""
			}
		}
		return nil
	})
}
func (m *Manager) Subscribe(ctx context.Context, principal string, p Params) (Subscription, error) {
	if e := validateParams(principal, p); e != nil {
		return Subscription{}, e
	}
	ttl, _ := parseTTL(p.TTL)
	id := subscriptionID(principal, p)
	gen, e := randomToken(16)
	if e != nil {
		return Subscription{}, e
	}
	now := time.Now().UTC()
	rec := &subscriptionRecord{ID: id, Generation: gen, Principal: principal, Params: p, Secret: p.Delivery.Secret, ExpiresAt: now.Add(time.Duration(ttl) * time.Millisecond), CreatedAt: now, UpdatedAt: now}
	if e = m.store.withLock(false, true, func(st *persistenceState) error {
		if !st.Enabled {
			return errf("disabled")
		}
		for k, s := range st.Subscriptions {
			if !s.ExpiresAt.After(now) {
				delete(st.Subscriptions, k)
			}
		}
		old := st.Subscriptions[id]
		if old == nil && len(st.Subscriptions) >= maxActiveSubs {
			return errf("capacity")
		}
		if old != nil {
			rec.CreatedAt = old.CreatedAt
			if old.Secret != rec.Secret {
				rec.PreviousSecret = old.Secret
				rec.RotateUntil = now.Add(secretRotateGrace)
			} else {
				rec.PreviousSecret = old.PreviousSecret
				rec.RotateUntil = old.RotateUntil
			}
		}
		st.Subscriptions[id] = rec
		return nil
	}); e != nil {
		return Subscription{}, e
	}
	if e = m.delivery.verify(ctx, rec); e != nil {
		return Subscription{}, e
	}
	e = m.store.withLock(false, true, func(st *persistenceState) error {
		if !st.Enabled {
			return errf("disabled")
		}
		s := st.Subscriptions[id]
		if s == nil || s.Generation != gen || !s.ExpiresAt.After(time.Now()) {
			return errf("stale")
		}
		s.CallbackVerified = true
		return nil
	})
	if e != nil {
		return Subscription{}, e
	}
	return Subscription{ID: id, ExpiresAt: rec.ExpiresAt, CallbackVerified: true}, nil
}
func (m *Manager) Unsubscribe(ctx context.Context, principal string, p Params) error {
	if ctx.Err() != nil {
		return errf("cancelled")
	}
	if validateID(principal) != nil || p.Arguments.QueueID != principal || p.Name != EventName || p.Delivery.Mode != "webhook" || validateCallbackURL(p.Delivery.URL) != nil {
		return errf("unknown")
	}
	return m.store.withLock(false, true, func(st *persistenceState) error { delete(st.Subscriptions, subscriptionID(principal, p)); return nil })
}
func (m *Manager) List(principal string) []Definition {
	if validateID(principal) != nil {
		return nil
	}
	return []Definition{{Name: EventName, Description: "A queued task is available; retrieve it using authorized queue tools."}}
}
func (m *Manager) Deliver(ctx context.Context, principal, queueID string, n Notice) error {
	if validateID(principal) != nil || principal != queueID || n.QueueID != queueID || validateID(n.EventID) != nil || validateID(n.JobID) != nil || (n.Mode != "answer" && n.Mode != "task") {
		return errf("unknown")
	}
	var ids []string
	if e := m.store.withLock(false, false, func(st *persistenceState) error {
		if !st.Enabled {
			return errf("disabled")
		}
		for id, s := range st.Subscriptions {
			if s.Principal == principal && s.CallbackVerified && s.ExpiresAt.After(time.Now()) {
				ids = append(ids, id)
			}
		}
		return nil
	}); e != nil {
		return e
	}
	if len(ids) == 0 {
		return errf("notfound")
	}
	sort.Strings(ids)
	var result error
	for _, id := range ids {
		if e := m.deliverOne(ctx, principal, queueID, id, n); e != nil {
			if result == nil || asEventsError(e).RetryAfter > 0 {
				result = e
			}
		}
	}
	return result
}
func (m *Manager) deliverOne(ctx context.Context, principal, q, id string, n Notice) error {
	var sub *subscriptionRecord
	var body []byte
	var attempt int
	var skip bool
	var outcome error
	key := deliveryKey(id, n.EventID)
	e := m.store.withLock(false, true, func(st *persistenceState) error {
		if !st.Enabled {
			return errf("disabled")
		}
		s := st.Subscriptions[id]
		if s == nil || !s.CallbackVerified || !s.ExpiresAt.After(time.Now()) {
			return errf("stale")
		}
		r := st.Deliveries[key]
		now := time.Now().UTC()
		if r != nil {
			if r.Acknowledged || r.Terminal {
				skip = true
				return nil
			}
			if r.InFlightUntil.After(now) {
				skip = true
				outcome = retryErr("in_flight", time.Second)
				return nil
			}
			if r.NextAttempt.After(now) {
				skip = true
				outcome = retryErr("retry", r.NextAttempt.Sub(now))
				return nil
			}
		}
		if r == nil {
			if len(st.Deliveries) >= maxDeliveryRecords {
				return errf("capacity")
			}
			r = &deliveryRecord{SubscriptionID: id, EventID: n.EventID, Body: eventBodyBytes(n)}
			st.Deliveries[key] = r
		}
		if r.Attempts >= 4 {
			r.Terminal = true
			skip = true
			outcome = errf("exhausted")
			return nil
		}
		r.Attempts++
		r.InFlightUntil = now.Add(maxHTTPTimeout + time.Second)
		r.UpdatedAt = now
		attempt = r.Attempts
		body = append([]byte(nil), r.Body...)
		cp := *s
		sub = &cp
		return nil
	})
	if e != nil {
		return e
	}
	if skip {
		return outcome
	}
	code, sendErr := m.delivery.deliverBytes(ctx, sub, n.EventID, body)
	e = m.store.withLock(false, true, func(st *persistenceState) error {
		if !st.Enabled {
			return errf("disabled")
		}
		s := st.Subscriptions[id]
		if s == nil || s.Generation != sub.Generation || !s.CallbackVerified || !s.ExpiresAt.After(time.Now()) {
			return errf("stale")
		}
		r := st.Deliveries[key]
		if r == nil || r.Attempts != attempt {
			return errf("stale")
		}
		r.InFlightUntil = time.Time{}
		r.LastCode = code
		r.UpdatedAt = time.Now().UTC()
		r.Acknowledged = sendErr == nil
		if sendErr != nil {
			c := asEventsError(sendErr).Code
			if (c == "network" || c == "server" || c == "rate_limited") && attempt <= len(retrySchedule) {
				delay := retrySchedule[attempt-1]
				r.NextAttempt = time.Now().Add(delay)
				outcome = retryErr(c, delay)
			} else {
				r.Terminal = true
				outcome = sendErr
			}
		}
		st.QueueStatus[queueKey(principal, q)] = &queueStatus{Attempts: r.Attempts, Acknowledged: r.Acknowledged, LastCode: code, LastEventID: n.EventID}
		return nil
	})
	if e != nil {
		return e
	}
	return outcome
}
func (m *Manager) Status(principal, q string) (Status, error) {
	if validateID(principal) != nil || principal != q {
		return Status{}, errf("unknown")
	}
	var out Status
	e := m.store.withLock(false, false, func(st *persistenceState) error {
		if st.Enabled {
			for _, s := range st.Subscriptions {
				if s.Principal == principal && s.ExpiresAt.After(time.Now()) {
					out.Subscribed = true
					out.CallbackVerified = out.CallbackVerified || s.CallbackVerified
				}
			}
		}
		if r := st.QueueStatus[queueKey(principal, q)]; r != nil {
			out.Attempts = r.Attempts
			out.Acknowledged = r.Acknowledged
			out.LastCode = r.LastCode
		}
		return nil
	})
	return out, e
}
