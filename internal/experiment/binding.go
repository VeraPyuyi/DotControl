package experiment

import "github.com/dotcontrol/dotcontrol/internal/connections"

// PrepareBinding freezes a caller's accepted profile snapshot before a model
// can invoke the helper. This record is not a dispatch intent and cannot make
// an existing v1 dispatch intent retryable. No SSH connection occurs here.
func (m *Manager) PrepareBinding(profile connections.ServerProfile, id string) (string, error) {
	if !ValidID(id) || profile.Validate() != nil {
		return "", failure("invalid_experiment_request")
	}
	wanted := makeIntent(profile, id)
	for _, suffix := range []string{".json", ".binding.json"} {
		stored, exists, e := m.readRecord(id, suffix)
		if e != nil {
			return "", e
		}
		if exists && stored.Binding != wanted.Binding {
			return "", failure("experiment_binding_conflict")
		}
		if exists && suffix == ".binding.json" {
			return wanted.Binding, nil
		}
	}
	if e := m.createRecord(wanted, ".binding.json"); e != nil {
		stored, exists, readErr := m.readRecord(id, ".binding.json")
		if readErr != nil || !exists {
			return "", failure("experiment_storage_unavailable")
		}
		if stored.Binding != wanted.Binding {
			return "", failure("experiment_binding_conflict")
		}
	}
	return wanted.Binding, nil
}
