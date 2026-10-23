package dotevents

import (
	"context"
	"sort"
	"time"
)

const verifiedCacheTTL = 5 * time.Minute

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
				s.VerifiedUntil = time.Time{}
				s.Generation = ""
			}
		}
		return nil
	})
}
func (m *Manager) Subscribe(ctx context.Context, principal string, p Params) (Subscription, error) {
	return m.subscribe(ctx, principal, p, time.Now)
}

func (m *Manager) subscribe(ctx context.Context, principal string, p Params, clock func() time.Time) (Subscription, error) {
	if e := validateParams(principal, p); e != nil {
		if asEventsError(e).Code == "callback" {
			if saveErr := m.store.withLock(false, true, func(st *persistenceState) error {
				if !st.Enabled {
					return errf("disabled")
				}
				st.LastSubscribe = &subscribeFailure{At: clock().UTC(), Principal: principal, Reason: "callback", Stage: "callback_url", Rule: callbackURLRule(p.Delivery.URL)}
				return nil
			}); saveErr != nil {
				return Subscription{}, saveErr
			}
		}
		return Subscription{}, e
	}
	ttl, _ := parseTTL(p.TTL)
	id := subscriptionID(principal, p)
	gen, e := randomToken(16)
	if e != nil {
		return Subscription{}, e
	}
	var previous *subscriptionRecord
	var cached bool
	rec := &subscriptionRecord{TrustEpoch: gen, ID: id, Generation: gen, Principal: principal, Params: p, Secret: p.Delivery.Secret}
	if e = m.store.withLock(false, true, func(st *persistenceState) error {
		if !st.Enabled {
			return errf("disabled")
		}
		// Eligibility and the new grant start at the serialized mutation, not
		// request arrival: acquiring the store lock may outlive the old lease.
		now := clock().UTC()
		rec.Permanent = ttl == 0
		if !rec.Permanent {
			rec.ExpiresAt = now.Add(time.Duration(ttl) * time.Millisecond)
		}
		rec.CreatedAt, rec.UpdatedAt = now, now
		for k, s := range st.Subscriptions {
			if !s.leaseActive(now) {
				delete(st.Subscriptions, k)
			}
		}
		old := st.Subscriptions[id]
		if old != nil {
			cp := *old
			previous = &cp
		}
		// Cache is scoped to authenticated principal plus exact URL and bounded
		// by both verification lifetime and subscription expiry.
		for _, s := range st.Subscriptions {
			if s.Principal == principal && s.Params.Delivery.URL == p.Delivery.URL && s.CallbackVerified && s.VerifiedUntil.After(now) && s.leaseActive(now) {
				cached = true
				rec.CallbackVerified = true
				rec.VerifiedUntil = boundedVerificationDeadline(s.VerifiedUntil, s.ExpiresAt, rec.ExpiresAt, now)
				break
			}
		}
		if old == nil && len(st.Subscriptions) >= maxActiveSubs {
			return errf("capacity")
		}
		if old != nil {
			// Only the exact continuously authorized binding retains mode trust.
			// Secret replacement deliberately starts a new epoch, even on a cache hit.
			if old.CallbackVerified && old.TrustEpoch != "" && old.Secret == rec.Secret {
				rec.TrustEpoch = old.TrustEpoch
			}
			rec.CreatedAt = old.CreatedAt
			if old.Secret != rec.Secret {
				rec.PreviousSecret = old.Secret
				rec.RotateUntil = now.Add(secretRotateGrace)
			} else {
				rec.PreviousSecret = old.PreviousSecret
				rec.RotateUntil = old.RotateUntil
			}
		}
		if cached && st.LastSubscribe != nil && st.LastSubscribe.Principal == principal {
			st.LastSubscribe = nil
		}
		st.Subscriptions[id] = rec
		return nil
	}); e != nil {
		return Subscription{}, e
	}
	if cached {
		err := m.store.withLock(false, true, func(st *persistenceState) error {
			now := clock().UTC()
			s := st.Subscriptions[id]
			if !st.Enabled || s == nil || s.Generation != gen || !s.CallbackVerified || !s.leaseActive(now) {
				return errf("stale")
			}
			// The cached path also reacquires the lock after persistence. A
			// delayed finalization cannot carry trust past the source lease.
			if previous != nil && !previous.leaseActive(now) {
				s.TrustEpoch, rec.TrustEpoch = gen, gen
			}
			return nil
		})
		if err != nil {
			return Subscription{}, err
		}
		return Subscription{Permanent: rec.Permanent, TrustEpoch: rec.TrustEpoch, ID: id, ExpiresAt: rec.ExpiresAt, CallbackVerified: true}, nil
	}
	if e = m.delivery.verify(ctx, rec); e != nil {
		// Restore a previously accepted refresh only if no revoke/refresh won
		// the generation race. Never extend its TTL or cache after failure.
		restoreErr := m.store.withLock(false, true, func(st *persistenceState) error {
			// Persist bounded, secret-free diagnostics before any rollback so an
			// operator can see why the platform was refused.
			code := asEventsError(e).Code
			st.LastSubscribe = &subscribeFailure{At: clock().UTC(), Principal: principal, Reason: diagnosticReason(code), Stage: verifyStage(code)}
			current := st.Subscriptions[id]
			if !st.Enabled {
				return errf("disabled")
			}
			if current == nil || !current.CallbackVerified && current.Generation == "" {
				return errf("stale")
			}
			if current.Generation != gen {
				// Another refresh owns this record. Preserve its state and the
				// original callback failure unless authorization was revoked.
				return nil
			}
			if previous != nil && previous.CallbackVerified && previous.leaseActive(clock()) {
				st.Subscriptions[id] = previous
			}
			return nil
		})
		if restoreErr != nil {
			return Subscription{}, restoreErr
		}
		return Subscription{}, e
	}
	e = m.store.withLock(false, true, func(st *persistenceState) error {
		if !st.Enabled {
			return errf("disabled")
		}
		now := clock().UTC()
		s := st.Subscriptions[id]
		if s == nil || s.Generation != gen || !s.leaseActive(now) {
			return errf("stale")
		}
		if previous != nil && !previous.leaseActive(now) {
			s.TrustEpoch = gen
			rec.TrustEpoch = gen
		}
		if st.LastSubscribe != nil && st.LastSubscribe.Principal == principal {
			st.LastSubscribe = nil
		}
		s.CallbackVerified = true
		s.UpdatedAt = now
		s.VerifiedUntil = boundedVerificationDeadline(s.UpdatedAt.Add(verifiedCacheTTL), s.ExpiresAt, s.ExpiresAt, s.UpdatedAt)
		return nil
	})
	if e != nil {
		return Subscription{}, e
	}
	return Subscription{Permanent: rec.Permanent, TrustEpoch: rec.TrustEpoch, ID: id, ExpiresAt: rec.ExpiresAt, CallbackVerified: true}, nil
}

// diagnosticReason accepts only stable categories, never arbitrary error text.
func diagnosticReason(code string) string {
	switch code {
	case "callback", "network", "timeout", "verification":
		return code
	default:
		return "unknown"
	}
}

// verifyStage maps an internal callback error code to the phase that failed:
// URL validation, DNS/network reachability, or the challenge exchange itself.
func verifyStage(code string) string {
	switch code {
	case "callback":
		return "callback_url"
	case "network", "timeout":
		return "network"
	case "verification":
		return "challenge"
	default:
		return "unknown"
	}
}

// Never extend an accepted verification proof on refresh, even after a
// backward clock step. Legacy deadlines need no guessed lock/IO allowance:
// each reuse is capped by both subscription grants and the current cache TTL.
func boundedVerificationDeadline(until, sourceExpiry, expiry, now time.Time) time.Time {
	for _, bound := range []time.Time{sourceExpiry, expiry, now.Add(verifiedCacheTTL)} {
		if !bound.IsZero() && bound.Before(until) {
			until = bound
		}
	}
	return until
}

func (m *Manager) Unsubscribe(ctx context.Context, principal string, p Params) error {
	if ctx.Err() != nil {
		return errf("cancelled")
	}
	if validateID(principal) != nil || p.Arguments.QueueID != principal || p.Name != EventName || p.Delivery.Mode != "webhook" || validateCallbackURL(p.Delivery.URL) != nil {
		return errf("unknown")
	}
	return m.store.withLock(false, true, func(st *persistenceState) error {
		delete(st.Subscriptions, subscriptionID(principal, p))
		for _, s := range st.Subscriptions {
			if s.Principal == principal && s.Params.Delivery.URL == p.Delivery.URL {
				s.VerifiedUntil = time.Time{}
			}
		}
		return nil
	})
}
func (m *Manager) List(principal string) []Definition {
	if validateID(principal) != nil {
		return []Definition{}
	}
	return []Definition{Catalog()[0]}
}

// Catalog returns the immutable discovery definition served over MCP Events.
func Catalog() []Definition {
	return []Definition{{
		Name:        EventName,
		Description: "Availability notices for an authorized paired queue.",
		Delivery:    []string{"webhook"},
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"queue_id": map[string]any{"type": "string", "description": "Authorized task queue ID."},
			},
			"required":             []string{"queue_id"},
			"additionalProperties": false,
		},
		PayloadSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"queue_id": map[string]any{"type": "string"},
				"job_id":   map[string]any{"type": "string"},
				"mode":     map[string]any{"type": "string", "enum": []string{"answer", "task"}},
			},
			"required":             []string{"queue_id", "job_id", "mode"},
			"additionalProperties": false,
		},
	}}
}
func (m *Manager) Deliver(ctx context.Context, principal, queueID string, n Notice) error {
	if n.OccurredAt.IsZero() || n.OccurredAt.Year() < 1 || n.OccurredAt.Year() > 9999 || validateID(principal) != nil || principal != queueID || n.QueueID != queueID || validateID(n.EventID) != nil || validateID(n.JobID) != nil || (n.Mode != "answer" && n.Mode != "task") {
		return errf("unknown")
	}
	var ids []string
	if e := m.store.withLock(false, false, func(st *persistenceState) error {
		if !st.Enabled {
			return errf("disabled")
		}
		for id, s := range st.Subscriptions {
			if s.Principal == principal && s.CallbackVerified && s.leaseActive(time.Now()) {
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
		if s == nil || !s.CallbackVerified || !s.leaseActive(time.Now()) {
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
		if s == nil || s.Generation != sub.Generation || !s.CallbackVerified || !s.leaseActive(time.Now()) {
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
		var verifiedUpdated time.Time
		if st.Enabled {
			for _, s := range st.Subscriptions {
				if s.Principal == principal && s.leaseActive(time.Now()) {
					out.Subscribed = true
					out.CallbackVerified = out.CallbackVerified || s.CallbackVerified
					if s.CallbackVerified {
						// Legacy verified leases remain real grants, but an empty
						// epoch supplies no evidence of durable mode trust.
						out.Bindings = append(out.Bindings, TrustedBinding{Permanent: s.Permanent, ID: s.ID, Epoch: s.TrustEpoch, Expires: s.ExpiresAt, Refreshed: s.UpdatedAt})
						if s.UpdatedAt.After(verifiedUpdated) {
							verifiedUpdated = s.UpdatedAt
						}
					}
				}
			}
		}
		if r := st.QueueStatus[queueKey(principal, q)]; r != nil {
			out.Attempts = r.Attempts
			out.Acknowledged = r.Acknowledged
			out.LastCode = r.LastCode
		}
		// Pre-epoch binaries could retain a failure after a later successful
		// refresh. Suppress only diagnostics strictly older than a verified
		// active binding for this queue; preserve newer refused renewals.
		if f := st.LastSubscribe; f != nil && f.Principal == principal && !verifiedUpdated.After(f.At) {
			out.SubscribeFailure = &SubscribeFailure{
				At:     f.At.UTC().Format(time.RFC3339),
				Reason: f.Reason,
				Stage:  f.Stage,
				Rule:   f.Rule,
			}
		}
		return nil
	})
	return out, e
}
