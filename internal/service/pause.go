package service

import (
	"context"
	"errors"
	"sync"
)

// Switch controls a gateway's admission AND all requests already forwarded.
// Cancellation propagates through HTTP to the device coordinator. Only that
// device can settle an already-issued native input; its receipt stays unknown.
type Switch struct {
	Backend Backend
	mu      sync.Mutex
	paused  bool
	next    uint64
	active  map[uint64]context.CancelFunc
}

func (s *Switch) SetPaused(paused bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paused = paused
	if paused {
		for _, cancel := range s.active {
			cancel()
		}
	}
}
func (s *Switch) Paused() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.paused }
func (s *Switch) Call(ctx context.Context, method string, r Request) (any, error) {
	s.mu.Lock()
	if s.paused && method != "devices" && method != "task" && method != "cancel" && method != "codex_job" && method != "codex_cancel" {
		s.mu.Unlock()
		return nil, errors.New("gateway_paused")
	}
	ctx, cancel := context.WithCancel(ctx)
	id := s.next
	s.next++
	if s.active == nil {
		s.active = make(map[uint64]context.CancelFunc)
	}
	s.active[id] = cancel
	s.mu.Unlock()
	defer func() { cancel(); s.mu.Lock(); delete(s.active, id); s.mu.Unlock() }()
	return s.Backend.Call(ctx, method, r)
}
