package dotbridge

import (
	"errors"
	"maps"
	"time"
)

// Invitations are internal preparation only: no network endpoint exposes them.
// Only hashes are persisted; deletion and identity creation share one commit.
type deviceInvitation struct {
	DeviceID string    `json:"device_id"`
	Expires  time.Time `json:"expires"`
}

func (s *Service) CreateInvitation(device string, ttl time.Duration) (string, error) {
	if !pairingID.MatchString(device) || ttl <= 0 || ttl > 15*time.Minute {
		return "", errors.New("pairing_invitation_invalid")
	}
	token, e := randomToken()
	if e != nil {
		return "", e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.state.Clients[device]; exists {
		return "", errors.New("client_exists")
	}
	previous := s.state.Invitations
	next := maps.Clone(previous)
	if next == nil {
		next = map[string]deviceInvitation{}
	}
	now := time.Now().UTC()
	for key, inv := range next {
		if !now.Before(inv.Expires) || inv.DeviceID == device {
			delete(next, key)
		}
	}
	if len(next) >= 128 {
		return "", errors.New("pairing_invitation_capacity")
	}
	next[hashToken(token)] = deviceInvitation{DeviceID: device, Expires: now.Add(ttl)}
	s.state.Invitations = next
	if e = s.saveState(); e != nil {
		s.state.Invitations = previous
		return "", e
	}
	return token, nil
}

func (s *Service) ConsumeInvitation(token, device string) (PairingFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := hashToken(token)
	inv, exists := s.state.Invitations[key]
	if !exists || device != inv.DeviceID || !time.Now().Before(inv.Expires) {
		return PairingFile{}, errors.New("pairing_invitation_rejected")
	}
	if _, exists = s.state.Clients[device]; exists {
		return PairingFile{}, errors.New("client_exists")
	}
	credential, e := randomToken()
	if e != nil {
		return PairingFile{}, e
	}
	now := time.Now().UTC()
	s.state.Clients[device] = ClientPolicy{ClientID: device, QueueID: device, TokenHash: hashToken(credential), CreatedAt: now}
	delete(s.state.Invitations, key)
	if e = s.saveState(); e != nil {
		delete(s.state.Clients, device)
		s.state.Invitations[key] = inv
		return PairingFile{}, e
	}
	// The recipient is responsible for retaining the returned credential. No
	// second file write can turn a durable consumption into a retryable error.
	return PairingFile{Version: APIVersion, ClientID: device, QueueID: device, Token: credential, Listen: s.listen, CreatedAt: now}, nil
}

func (s *Service) RevokeInvitation(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := hashToken(token)
	previous, exists := s.state.Invitations[key]
	if !exists {
		return errors.New("pairing_invitation_rejected")
	}
	delete(s.state.Invitations, key)
	if e := s.saveState(); e != nil {
		s.state.Invitations[key] = previous
		return e
	}
	return nil
}
