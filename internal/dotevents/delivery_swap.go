package dotevents

// SetDeliveryForTest replaces the outbound transport for offline integration
// tests. Only tests construct it with a fixture HTTP client.
func (m *Manager) SetDeliveryForTest(d *Delivery) {
	if d == nil {
		d = &Delivery{}
	}
	m.delivery = d
}
