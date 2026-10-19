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
	mu        sync.Mutex
	ctx       context.Context
	cancel    context.CancelFunc
	root      string
	listen    string
	vault     vault.Store
	queue     QueueAPI
	events    EventsAPI
	mcpQueue  string
	mcpEpoch  uint64
	state     PersistedState
	recovered bool
	worker    *worker
	paused    func() bool
	revoked   func() bool
}

type Config struct {
	Root      string
	Listen    string
	Vault     vault.Store
	MCPQueue  string
	MCPEpoch  uint64
	Paused    func() bool
	Revoked   func() bool
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
	eventsMgr, err := dotevents.Open(eventsDir, key, &dotevents.Delivery{})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	s := &Service{
		ctx:      ctx,
		cancel:   cancel,
		root:     cfg.Root,
		listen:   cfg.Listen,
		vault:    cfg.Vault,
		queue:    wrapQueue(queueStore),
		events:   wrapEvents(eventsMgr),
		mcpQueue: cfg.MCPQueue,
		mcpEpoch: cfg.MCPEpoch,
		paused:   cfg.Paused,
		revoked:  cfg.Revoked,
		state:    PersistedState{Version: APIVersion, Clients: map[string]ClientPolicy{}},
	}
	if err := s.loadState(); err != nil {
		cancel()
		return nil, err
	}
	if err := s.recoverOnce(); err != nil {
		cancel()
		return nil, err
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
	s.mu.Lock()
	defer s.mu.Unlock()
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
	if !s.state.Enabled {
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
	defer s.mu.Unlock()
	policy, ok := s.state.Clients[clientID]
	if !ok || policy.Revoked {
		return StatusDTO{Version: APIVersion, Enabled: s.state.Enabled, Paired: false, Ready: false, Code: "not_paired", Capabilities: CapPolicy{}}, nil
	}
	return s.statusDTO(policy), nil
}

func (s *Service) statusDTO(policy ClientPolicy) StatusDTO {
	now := time.Now().UTC()
	eventsStatus, _ := s.events.Status(policy.QueueID, policy.QueueID)
	sub := SubscriptionDTO{State: "inactive"}
	if eventsStatus.Subscribed && eventsStatus.CallbackVerified {
		sub.State = "active"
	}
	ready := false
	code := "probe_required"
	var verifiedAt, expiresAt *time.Time
	if s.state.Enabled && policy.ProbeVerified.After(time.Time{}) && policy.ProbeExpires.After(now) {
		ready = eventsStatus.Subscribed && eventsStatus.CallbackVerified
		if ready {
			code = "ready"
			verifiedAt = &policy.ProbeVerified
			expiresAt = &policy.ProbeExpires
			if sub.State == "active" {
				sub.ExpiresAt = expiresAt
			}
		}
	}
	return StatusDTO{
		Version:      APIVersion,
		Enabled:      s.state.Enabled,
		Paired:       true,
		Ready:        ready,
		Code:         code,
		VerifiedAt:   verifiedAt,
		ExpiresAt:    expiresAt,
		Subscription: sub,
		Capabilities: policy.Capabilities,
	}
}

func (s *Service) Pair(clientID string) (PairingFile, string, error) {
	if clientID == "" {
		clientID = "client." + time.Now().UTC().Format("20060102150405")
	}
	token, err := randomToken()
	if err != nil {
		return PairingFile{}, "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
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
	return s.events.List(principal)
}

func (s *Service) EventsSubscribe(ctx context.Context, principal string, params dotevents.Params) (dotevents.Subscription, error) {
	if err := s.gate(); err != nil {
		return dotevents.Subscription{}, err
	}
	return s.events.Subscribe(ctx, principal, params)
}

func (s *Service) EventsUnsubscribe(ctx context.Context, principal string, params dotevents.Params) error {
	if err := s.gate(); err != nil {
		return err
	}
	return s.events.Unsubscribe(ctx, principal, params)
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
	path := filepath.Join(s.root, "bridge.state.json")
	data, err := os.ReadFile(path)
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
	if err := os.MkdirAll(s.root, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(s.root, "bridge.state.json")
	return os.WriteFile(path, data, 0600)
}

func (s *Service) submit(client ClientPolicy, req SubmitRequest) (JobDTO, int, error) {
	if err := s.gate(); err != nil {
		return JobDTO{}, 0, err
	}
	status := s.statusDTO(policy)
	if !status.Ready && req.Mode != dotqueue.ModeAnswer {
		return JobDTO{}, 0, errors.New("probe_required")
	}
	if !status.Ready {
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
	dto := toJobDTO(job)
	dto.Accepted = true
	return dto, 202, nil
}

func (s *Service) getJob(client ClientPolicy, jobID string) (JobDTO, error) {
	if err := s.gate(); err != nil {
		return JobDTO{}, err
	}
	job, err := s.queue.Get(client.ClientID, jobID)
	if err != nil {
		return JobDTO{}, err
	}
	return toJobDTO(job), nil
}

func (s *Service) byRequest(client ClientPolicy, requestID string) (JobDTO, error) {
	if err := s.gate(); err != nil {
		return JobDTO{}, err
	}
	job, err := s.queue.ByRequest(client.ClientID, requestID)
	if err != nil {
		return JobDTO{}, err
	}
	return toJobDTO(job), nil
}

func (s *Service) cancelJob(client ClientPolicy, jobID string) (JobDTO, error) {
	if err := s.gate(); err != nil {
		return JobDTO{}, err
	}
	job, err := s.queue.Cancel(client.ClientID, jobID)
	if err != nil {
		return JobDTO{}, err
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
	}
	return dto
}
