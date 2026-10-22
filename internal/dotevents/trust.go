package dotevents

// TrustedBindings returns only verified grants for this authenticated queue.
// This internal API must never be serialized into the public status response.
func (m *Manager) TrustedBindings(principal string) ([]TrustedBinding, error) {
	status, err := m.Status(principal, principal)
	return status.Bindings, err
}
