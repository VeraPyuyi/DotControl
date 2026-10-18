package main

import (
	"context"
	"encoding/json"
	"github.com/dotcontrol/dotcontrol/internal/config"
	"github.com/dotcontrol/dotcontrol/internal/onboard"
	"github.com/dotcontrol/dotcontrol/internal/tunnel"
)

// Network is accessible only through the protected local wizard. It saves the
// desired route; the existing supervisor owns the decision when to reconnect.
func (o *runtimeOwner) Network(_ context.Context, request onboard.NetworkRequest) (any, error) {
	network := config.Network{Mode: request.Mode, ProxyURL: request.ProxyURL}
	if network.Mode == "" {
		network.Mode = "auto"
	}
	if err := config.ValidateNetwork(network); err != nil {
		return nil, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	next := o.cfg
	next.Network = network
	if err := config.Save(o.dir, next); err != nil {
		return nil, err
	}
	// UpdateNetwork performs only the already-completed validation and an
	// in-memory queue update; it never starts, stops or authorizes execution.
	if o.sup != nil {
		if err := o.sup.UpdateNetwork(network); err != nil {
			return nil, err
		}
	}
	o.cfg = next
	return map[string]any{"saved": true, "network": network, "reconnect": "queued_until_restart"}, nil
}

// The official doctor validates configuration. Only the current supervisor's
// poll evidence can report cloud readiness; older readyz-only agents cannot.
func doctorCloudStatus(data []byte) tunnel.Status {
	var payload struct {
		Tunnel tunnel.Status `json:"tunnel"`
	}
	if json.Unmarshal(data, &payload) != nil {
		return tunnel.Status{CloudState: "unavailable"}
	}
	s := payload.Tunnel
	if s.CloudState == "" {
		s.CloudState = "unconfirmed"
	}
	s.Ready = s.Ready && s.LocalReady && s.LastSuccessfulPollAt != nil && (s.CloudState == "online" || s.CloudState == "backpressured") && s.Error == ""
	return s
}
