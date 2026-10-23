package dotbridge

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/dotevents"
	"github.com/dotcontrol/dotcontrol/internal/dotqueue"
	"github.com/dotcontrol/dotcontrol/internal/vault"
)

// Service owns bridge lifecycle, local API auth, and delivery worker wiring.
type Service struct {
	mu            sync.Mutex
	capacityEpoch uint64 // guarded by mu; invalidates reads across enable/disable
	ctx           context.Context
	cancel        context.CancelFunc
	root          string
	listen        string
	vault         vault.Store
	queue         QueueAPI
	events        EventsAPI
	mcpQueue      string
	subscriptions map[string]subscriptionProof
	claims        map[string]claimBinding
	state         PersistedState
	recovered     bool
	worker        *worker
	paused        func() bool
	revoked       func() bool
}

type Config struct {
	Root           string
	Listen         string
	Vault          vault.Store
	MCPQueue       string
	Paused         func() bool
	Revoked        func() bool
	EventsDelivery *dotevents.Delivery
}

func Open(ctx context.Context, cfg Config) (*Service, error) {
	if cfg.Vault == nil || cfg.Root == "" || cfg.Listen == "" {
		return nil, errors.New("bridge_config_invalid")
	}
	encoded, err := cfg.Vault.Get(StateKeyName)
	if err != nil {
		return nil, errors.New("bridge_state_key_missing")
	}
	key, err := decodeStateKey(encoded)
	if err != nil {
		return nil, err
	}
	queueDir := filepath.Join(cfg.Root, "queue")
	eventsDir := filepath.Join(cfg.Root, "events")
	queueStore, err := dotqueue.Open(queueDir, key)
	if err != nil {
		return nil, err
	}
	delivery := cfg.EventsDelivery
	if delivery == nil {
		delivery = &dotevents.Delivery{}
	}
	eventsMgr, err := dotevents.Open(eventsDir, key, delivery)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	s := &Service{
		ctx:           ctx,
		cancel:        cancel,
		root:          cfg.Root,
		listen:        cfg.Listen,
		vault:         cfg.Vault,
		queue:         wrapQueue(queueStore),
		events:        wrapEvents(eventsMgr),
		mcpQueue:      cfg.MCPQueue,
		subscriptions: map[string]subscriptionProof{},
		claims:        map[string]claimBinding{},
		paused:        cfg.Paused,
		revoked:       cfg.Revoked,
		state:         PersistedState{Version: APIVersion, Clients: map[string]ClientPolicy{}},
	}
	if err := s.loadState(); err != nil {
		cancel()
		return nil, err
	}
	if s.state.Enabled {
		s.mu.Lock()
		defer s.mu.Unlock()
		if err := s.queue.SetEnabled(true); err != nil {
			cancel()
			return nil, err
		}
		if err := s.events.SetEnabled(true); err != nil {
			cancel()
			return nil, err
		}
		if err := s.recoverOnce(); err != nil {
			cancel()
			return nil, err
		}
	}
	s.worker = newWorker(s)
	s.worker.Start()
	return s, nil
}

func (s *Service) Close() error {
	if s.cancel != nil {
		s.cancel()
	}
	if s.worker != nil {
		s.worker.Wait()
	}
	return nil
}

func (s *Service) recoverOnce() error {
	if s.recovered {
		return nil
	}
	if err := s.queue.Recover(); err != nil {
		return err
	}
	s.recovered = true
	return nil
}

func (s *Service) gate() error {
	if s.revoked != nil && s.revoked() {
		return errors.New("access_revoked")
	}
	if s.paused != nil && s.paused() {
		return errors.New("bridge_paused")
	}
	s.mu.Lock()
	enabled := s.state.Enabled
	s.mu.Unlock()
	if !enabled {
		return errors.New("bridge_disabled")
	}
	return nil
}

func (s *Service) Enable() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.queue.Initialize(false); err != nil {
		return err
	}
	if err := s.queue.SetEnabled(true); err != nil {
		return err
	}
	if err := s.events.SetEnabled(true); err != nil {
		return err
	}
	if err := s.recoverOnce(); err != nil {
		return err
	}
	s.state.Enabled = true
	s.capacityEpoch++
	return s.saveState()
}

func (s *Service) Disable() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.queue.SetEnabled(false); err != nil {
		return err
	}
	if err := s.events.SetEnabled(false); err != nil {
		return err
	}
	s.state.Enabled = false
	s.capacityEpoch++
	return s.saveState()
}

// bridgeCapacity captures a candidate only. Callers publish it under mu using
// the same final authorization snapshot as the rest of their response.
func (s *Service) bridgeCapacity() (*CapacityDTO, uint64, error) {
	s.mu.Lock()
	enabled, epoch := s.capacityAllowedLocked(), s.capacityEpoch
	s.mu.Unlock()
	if !enabled {
		return nil, epoch, nil
	}
	cap, err := s.queue.Capacity()
	if err != nil {
		return nil, epoch, err
	}
	return &CapacityDTO{Limit: cap.Limit, Retained: cap.Retained, Remaining: cap.Remaining}, epoch, nil
}

func (s *Service) capacityAllowedLocked() bool {
	return s.state.Enabled && (s.paused == nil || !s.paused()) && (s.revoked == nil || !s.revoked())
}

func (s *Service) AdminStatus() (map[string]any, error) {
	cap, epoch, err := s.bridgeCapacity()
	s.mu.Lock()
	defer s.mu.Unlock()
	allowed := s.capacityAllowedLocked() && epoch == s.capacityEpoch
	if err != nil && allowed {
		return nil, err
	}
	paired := 0
	for _, policy := range s.state.Clients {
		if !policy.Revoked {
			paired++
		}
	}
	enabled := s.state.Enabled
	out := map[string]any{
		"version": APIVersion,
		"enabled": enabled,
		"paired":  paired > 0,
		"clients": paired,
	}
	if allowed && cap != nil {
		out["capacity"] = cap
	}
	return out, nil
}

func (s *Service) StatusForClient(clientID string) (StatusDTO, error) {
	s.mu.Lock()
	policy, ok := s.state.Clients[clientID]
	enabled := s.state.Enabled
	s.mu.Unlock()
	if !ok || policy.Revoked {
		return StatusDTO{Version: APIVersion, Enabled: enabled, Code: "not_paired"}, nil
	}
	return s.statusDTO(policy), nil
}
func (s *Service) statusDTO(policy ClientPolicy) StatusDTO {
	// Read policy and subscription together after external I/O. A renewal must
	// never combine its subscription with a previously captured proof.
	eventStatus, e := s.events.Status(policy.QueueID, policy.QueueID)
	gateOK := s.clientGate(policy.ClientID) == nil
	var capacity *CapacityDTO
	var epoch uint64
	var capErr error
	if gateOK {
		capacity, epoch, capErr = s.bridgeCapacity()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, exists := s.state.Clients[policy.ClientID]
	sub := s.subscriptions[current.QueueID]
	now := time.Now()
	active := sub.leaseActive(now)
	if sub.TrustEpoch != "" {
		active = false
		for _, b := range eventStatus.Bindings {
			if b.ID == sub.BindingID && b.Epoch == sub.TrustEpoch && sub.AuthHash == current.TokenHash && sub.Consumer == s.mcpQueue && (b.Permanent || now.Before(b.Expires)) {
				sub = intersectLease(sub, b)
				sub.Refreshed = b.Refreshed
				// Both stores must authorize the effective lease. An event-only
				// renewal cannot extend the bridge's last committed grant.
				active = sub.leaseActive(now)
			}
		}
	}
	out := StatusDTO{Version: APIVersion, Enabled: s.state.Enabled, Paired: exists && !current.Revoked, Code: "probe_required", Subscription: SubscriptionDTO{State: "inactive"}}
	if active {
		out.Subscription = SubscriptionDTO{State: "active", LastRefreshedAt: &sub.Refreshed, RenewalState: "current"}
		if !sub.Permanent {
			out.Subscription.ExpiresAt, out.Subscription.RefreshBefore = &sub.Expires, &sub.Expires
		}
	} else if sub.Generation == 0 && sub.Expires.IsZero() && e == nil && gateOK && exists && !current.Revoked && current.TokenHash == policy.TokenHash && s.capacityAllowedLocked() {
		// Old binaries did not persist the bridge subscription map. Report the
		// actual verified legacy lease without adopting it as authorization.
		// Only an authenticated EventsSubscribe can create the new trust record;
		// active stays false so consumed legacy proofs cannot authorize work.
		for _, b := range eventStatus.Bindings {
			if b.Epoch == "" && now.Before(b.Expires) && (out.Subscription.ExpiresAt == nil || b.Expires.After(*out.Subscription.ExpiresAt) || b.Expires.Equal(*out.Subscription.ExpiresAt) && b.Refreshed.After(*out.Subscription.LastRefreshedAt)) {
				expires, refreshed := b.Expires, b.Refreshed
				out.Subscription = SubscriptionDTO{State: "active", ExpiresAt: &expires, RefreshBefore: &expires, LastRefreshedAt: &refreshed, RenewalState: "current"}
			}
		}
	}
	if eventStatus.SubscribeFailure != nil && gateOK && exists && !current.Revoked && current.TokenHash == policy.TokenHash && s.capacityAllowedLocked() {
		// A refused subscription is not silently "inactive": expose why the
		// callback verification failed so an operator can fix the cause.
		out.Subscription.RenewalState = "failed"
		out.SubscribeFailure = &SubscribeFailureDTO{
			At:     eventStatus.SubscribeFailure.At,
			Reason: eventStatus.SubscribeFailure.Reason,
			Stage:  eventStatus.SubscribeFailure.Stage,
			Rule:   eventStatus.SubscribeFailure.Rule,
		}
	}
	if gateOK && exists && !current.Revoked && current.TokenHash == policy.TokenHash && s.capacityAllowedLocked() && epoch == s.capacityEpoch && capErr == nil {
		out.Capacity = capacity
	}
	if !sub.Expires.IsZero() && !active {
		out.Code = "subscription_expired"
		out.Subscription.State = "expired"
	}
	if !s.state.Enabled {
		out.Code = "disabled"
	}
	if s.paused != nil && s.paused() {
		out.Code = "bridge_paused"
	}
	if current.Revoked || (s.revoked != nil && s.revoked()) {
		out.Code = "access_revoked"
	}
	if !gateOK || !s.state.Enabled || !exists || current.Revoked || !active || e != nil || !eventStatus.Subscribed || !eventStatus.CallbackVerified || (s.paused != nil && s.paused()) || (s.revoked != nil && s.revoked()) {
		return out
	}
	for _, mode := range []string{dotqueue.ModeAnswer, dotqueue.ModeTask} {
		proof := current.proof(mode)
		trusted := proof.TrustEpoch != "" && proof.TrustEpoch == sub.TrustEpoch
		if proof.Verified.IsZero() || !proof.Consumed || proof.Generation != sub.Generation || ((proof.TrustEpoch != "" || sub.TrustEpoch != "") && !trusted) || (!trusted && (!now.Before(proof.Expires) || !proof.SubscriptionExpires.Equal(sub.Expires))) {
			continue
		}
		expiry := proof.Expires
		if trusted {
			expiry = sub.Expires
		}
		if !sub.Permanent && (expiry.IsZero() || sub.Expires.Before(expiry)) {
			expiry = sub.Expires
		}
		if !expiry.IsZero() && (out.ExpiresAt == nil || expiry.Before(*out.ExpiresAt)) {
			out.ExpiresAt = &expiry
		}
		verified := proof.Verified
		if out.VerifiedAt == nil || verified.Before(*out.VerifiedAt) {
			out.VerifiedAt = &verified
		}
		if mode == dotqueue.ModeTask {
			out.Capabilities.Tasks = true
		} else {
			out.Capabilities.Text = true
		}
		out.Ready, out.Code = true, "ready"
	}
	return out
}

func (s *Service) Pair(clientID string) (PairingFile, string, error) {
	if clientID == "" {
		id, e := randomToken()
		if e != nil {
			return PairingFile{}, "", e
		}
		clientID = "client." + id
	}
	token, err := randomToken()
	if err != nil {
		return PairingFile{}, "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.state.Clients[clientID]; exists {
		return PairingFile{}, "", errors.New("client_exists")
	}
	policy := ClientPolicy{
		ClientID:     clientID,
		QueueID:      clientID,
		TokenHash:    hashToken(token),
		CreatedAt:    time.Now().UTC(),
		Capabilities: CapPolicy{Text: false, Tasks: false},
	}
	s.state.Clients[clientID] = policy
	if err := s.saveState(); err != nil {
		return PairingFile{}, "", err
	}
	file := PairingFile{Version: APIVersion, ClientID: clientID, QueueID: clientID, Token: token, Listen: s.listen, CreatedAt: policy.CreatedAt}
	path, err := writePairingFile(s.root, file)
	return file, path, err
}

func (s *Service) RevokeClient(clientID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	policy, ok := s.state.Clients[clientID]
	if !ok {
		return errors.New("client_not_found")
	}
	policy.Revoked = true
	s.state.Clients[clientID] = policy
	delete(s.subscriptions, policy.QueueID)
	return s.saveState()
}

func (s *Service) clientFromToken(token string) (ClientPolicy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, policy := range s.state.Clients {
		if policy.Revoked {
			continue
		}
		if tokenMatches(policy.TokenHash, token) {
			return policy, nil
		}
	}
	return ClientPolicy{}, errors.New("unauthorized")
}

func (s *Service) MCPPrincipal() (string, string, error) {
	if s.mcpQueue == "" {
		return "", "", errors.New("mcp_queue_unconfigured")
	}
	return s.mcpQueue, s.mcpQueue, nil
}

// EventsList advertises the catalog whenever the bridge is enabled. Queue
// authorization is enforced on subscribe, not on discovery: the authenticated
// MCP consumer must be able to discover events before it can pick a queue.
func (s *Service) EventsList(principal string) []dotevents.Definition {
	if s.gate() != nil {
		return []dotevents.Definition{}
	}
	return dotevents.Catalog()
}
func (s *Service) EventsSubscribe(ctx context.Context, principal string, params dotevents.Params) (dotevents.Subscription, error) {
	queue := params.Arguments.QueueID
	if e := s.authorizeQueue(queue); e != nil {
		return dotevents.Subscription{}, e
	}
	s.mu.Lock()
	epoch := s.capacityEpoch
	s.mu.Unlock()
	sub, e := s.events.Subscribe(ctx, queue, params)
	if e != nil {
		return sub, e
	}
	if e = s.authorizeQueue(queue); e != nil {
		return dotevents.Subscription{}, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.state.Clients[queue]
	if p.Revoked {
		return dotevents.Subscription{}, errors.New("access_revoked")
	}
	if !s.capacityAllowedLocked() || epoch != s.capacityEpoch {
		return dotevents.Subscription{}, errors.New("bridge_disabled")
	}
	if reader, ok := s.events.(interface {
		TrustedBindings(string) ([]dotevents.TrustedBinding, error)
	}); ok {
		bindings, readErr := reader.TrustedBindings(queue)
		matched := false
		for _, b := range bindings {
			if b.ID == sub.ID && b.Epoch == sub.TrustEpoch && b.Permanent == sub.Permanent && b.Expires.Equal(sub.ExpiresAt) {
				matched = true
			}
		}
		if readErr != nil || !matched {
			return dotevents.Subscription{}, errors.New("subscription_changed")
		}
	}
	old := s.subscriptions[queue]
	generation := old.Generation + 1
	if sub.TrustEpoch != "" && sub.TrustEpoch == old.TrustEpoch && old.BindingID == sub.ID && old.AuthHash == p.TokenHash && old.Consumer == s.mcpQueue {
		generation = old.Generation
	}
	s.subscriptions[queue] = subscriptionProof{Permanent: sub.Permanent, Expires: sub.ExpiresAt, Generation: generation, BindingID: sub.ID, TrustEpoch: sub.TrustEpoch, AuthHash: p.TokenHash, Consumer: s.mcpQueue, Refreshed: time.Now().UTC()}
	if generation != old.Generation {
		p.Capabilities = CapPolicy{}
		p.ProbeExpires = time.Time{}
		p.TaskProbe.Expires = time.Time{}
	}
	s.state.Clients[queue] = p
	return sub, s.saveState()
}
func (s *Service) EventsUnsubscribe(ctx context.Context, principal string, params dotevents.Params) error {
	queue := params.Arguments.QueueID
	if e := s.authorizeQueue(queue); e != nil {
		return e
	}
	e := s.events.Unsubscribe(ctx, queue, params)
	s.mu.Lock()
	old := s.subscriptions[queue]
	old.Expires = time.Time{}
	old.Permanent = false
	old.Generation++
	s.subscriptions[queue] = old
	if saveErr := s.saveState(); e == nil {
		e = saveErr
	}
	s.mu.Unlock()
	if post := s.authorizeQueue(queue); post != nil {
		return post
	}
	return e
}

func (s *Service) ClaimTask(queueID, jobID, workerID string) (dotqueue.Claim, error) {
	return s.claimTask(queueID, jobID, workerID)
}

func (s *Service) ProgressTask(jobID, token, state, summary string) (dotqueue.Job, error) {
	return s.progressTask(jobID, token, state, summary)
}

func (s *Service) CompleteTask(jobID, token string, result dotqueue.Result) (dotqueue.Job, error) {
	return s.completeTask(jobID, token, result)
}

func (s *Service) TaskStatus(principal, jobID, token string) (dotqueue.Job, error) {
	return s.taskStatus(principal, jobID, token)
}

func (s *Service) loadState() error {
	data, err := securelyReadBridgeState(s.root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var st PersistedState
	if json.Unmarshal(data, &st) != nil {
		return errors.New("bridge_state_corrupt")
	}
	if st.Clients == nil {
		st.Clients = map[string]ClientPolicy{}
	}
	s.state = st
	if st.Version != APIVersion {
		return errors.New("bridge_state_corrupt")
	}
	if st.Subscriptions != nil {
		for queue, sub := range st.Subscriptions {
			policy, ok := st.Clients[queue]
			if !ok || policy.QueueID != queue || sub.Generation == 0 || !completeSubscriptionProof(sub) {
				return errors.New("bridge_state_corrupt")
			}
		}
		s.subscriptions = st.Subscriptions
	}
	return nil
}

func (s *Service) saveState() error {
	s.state.Subscriptions = s.subscriptions
	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	return securelyWriteBridgeState(s.root, data)
}

func (s *Service) submit(client ClientPolicy, req SubmitRequest) (JobDTO, int, error) {
	if err := s.gate(); err != nil {
		return JobDTO{}, 0, err
	}
	if e := s.clientGate(client.ClientID); e != nil {
		return JobDTO{}, 0, e
	}
	status := s.statusDTO(client)
	if req.Mode == dotqueue.ModeTask {
		if !status.Capabilities.Tasks {
			return JobDTO{}, 0, errors.New("task_probe_required")
		}
	} else if !status.Capabilities.Text {
		return JobDTO{}, 0, errors.New("probe_required")
	}
	sub := dotqueue.Submission{
		RequestID: req.RequestID,
		Mode:      req.Mode,
		SessionID: req.SessionID,
		Task:      req.Task,
	}
	for _, m := range req.Messages {
		sub.Messages = append(sub.Messages, dotqueue.Message{Role: m.Role, Content: m.Content})
	}
	job, err := s.queue.Submit(client.ClientID, sub)
	if err != nil {
		return JobDTO{}, 0, err
	}
	if e := s.clientGate(client.ClientID); e != nil {
		return JobDTO{}, 0, e
	}
	postStatus := s.statusDTO(client)
	if (req.Mode == dotqueue.ModeTask && !postStatus.Capabilities.Tasks) || (req.Mode != dotqueue.ModeTask && !postStatus.Capabilities.Text) {
		return JobDTO{}, 0, errors.New("probe_required")
	}
	dto := toJobDTO(job)
	dto.Accepted = true
	return dto, 202, nil
}

func (s *Service) getJob(client ClientPolicy, jobID string) (JobDTO, error) {
	if err := s.gate(); err != nil {
		return JobDTO{}, err
	}
	if e := s.clientGate(client.ClientID); e != nil {
		return JobDTO{}, e
	}
	job, err := s.queue.Get(client.ClientID, jobID)
	if err != nil {
		return JobDTO{}, err
	}
	if e := s.clientGate(client.ClientID); e != nil {
		return JobDTO{}, e
	}
	return toJobDTO(job), nil
}

func (s *Service) byRequest(client ClientPolicy, requestID string) (JobDTO, error) {
	if err := s.gate(); err != nil {
		return JobDTO{}, err
	}
	if e := s.clientGate(client.ClientID); e != nil {
		return JobDTO{}, e
	}
	job, err := s.queue.ByRequest(client.ClientID, requestID)
	if err != nil {
		return JobDTO{}, err
	}
	if e := s.clientGate(client.ClientID); e != nil {
		return JobDTO{}, e
	}
	return toJobDTO(job), nil
}

func (s *Service) cancelJob(client ClientPolicy, jobID string) (JobDTO, error) {
	if err := s.gate(); err != nil {
		return JobDTO{}, err
	}
	if e := s.clientGate(client.ClientID); e != nil {
		return JobDTO{}, e
	}
	job, err := s.queue.Cancel(client.ClientID, jobID)
	if err != nil {
		return JobDTO{}, err
	}
	if e := s.clientGate(client.ClientID); e != nil {
		return JobDTO{}, e
	}
	return toJobDTO(job), nil
}

func toJobDTO(job dotqueue.Job) JobDTO {
	dto := JobDTO{
		Version:         APIVersion,
		JobID:           job.ID,
		RequestID:       job.RequestID,
		RequestDigest:   job.RequestDigest,
		Mode:            job.Mode,
		SessionID:       job.SessionID,
		State:           job.State,
		Summary:         job.Summary,
		CreatedAt:       job.CreatedAt,
		UpdatedAt:       job.UpdatedAt,
		CancelRequested: job.CancelRequested,
		ErrorCode:       job.ErrorCode,
	}
	if job.Result != nil {
		dto.Result = &ResultDTO{Text: job.Result.Text, Cancelled: job.Result.Cancelled, ErrorCode: job.Result.ErrorCode}
		for _, artifact := range job.Result.Artifacts {
			dto.Result.Artifacts = append(dto.Result.Artifacts, ArtifactDTO{URL: artifact.URL, Label: artifact.Label})
		}
	}
	return dto
}
