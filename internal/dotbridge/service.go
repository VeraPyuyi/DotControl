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
	return s.saveState()
}

func (s *Service) AdminStatus() (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	paired := 0
	for _, policy := range s.state.Clients {
		if !policy.Revoked {
			paired++
		}
	}
	return map[string]any{
		"version": APIVersion,
		"enabled": s.state.Enabled,
		"paired":  paired > 0,
		"clients": paired,
	}, nil
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
	s.mu.Lock()
	defer s.mu.Unlock()
	current, exists := s.state.Clients[policy.ClientID]
	sub := s.subscriptions[current.QueueID]
	now := time.Now()
	active := now.Before(sub.Expires)
	out := StatusDTO{Version: APIVersion, Enabled: s.state.Enabled, Paired: exists && !current.Revoked, Code: "probe_required", Subscription: SubscriptionDTO{State: "inactive"}}
	if active {
		out.Subscription = SubscriptionDTO{State: "active", ExpiresAt: &sub.Expires}
	}
	if !gateOK || !s.state.Enabled || !exists || current.Revoked || !active || e != nil || !eventStatus.Subscribed || !eventStatus.CallbackVerified || (s.paused != nil && s.paused()) || (s.revoked != nil && s.revoked()) {
		return out
	}
	for _, mode := range []string{dotqueue.ModeAnswer, dotqueue.ModeTask} {
		proof := current.proof(mode)
		if proof.Verified.IsZero() || !proof.Consumed || proof.Generation != sub.Generation || !now.Before(proof.Expires) || !proof.SubscriptionExpires.Equal(sub.Expires) {
			continue
		}
		expiry := proof.Expires
		if sub.Expires.Before(expiry) {
			expiry = sub.Expires
		}
		if out.ExpiresAt == nil || expiry.Before(*out.ExpiresAt) {
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

func (s *Service) EventsList(principal string) []dotevents.Definition {
	if s.gate() != nil {
		return nil
	}
	return []dotevents.Definition{{Name: dotevents.EventName, Description: "Availability notices for an authorized paired queue."}}
}
func (s *Service) EventsSubscribe(ctx context.Context, principal string, params dotevents.Params) (dotevents.Subscription, error) {
	queue := params.Arguments.QueueID
	if e := s.authorizeQueue(queue); e != nil {
		return dotevents.Subscription{}, e
	}
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
	old := s.subscriptions[queue]
	s.subscriptions[queue] = subscriptionProof{Expires: sub.ExpiresAt, Generation: old.Generation + 1}
	// Keep consumed timestamps for audit/replay protection. Generation binding
	// invalidates both modes without erasing their first verification time.
	p.Capabilities = CapPolicy{}
	p.ProbeExpires = time.Time{}
	p.TaskProbe.Expires = time.Time{}
	s.state.Clients[queue] = p
	return sub, nil
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
	old.Generation++
	s.subscriptions[queue] = old
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
	return nil
}

func (s *Service) saveState() error {
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
