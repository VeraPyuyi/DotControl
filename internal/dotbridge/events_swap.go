package dotbridge

import "github.com/dotcontrol/dotcontrol/internal/dotevents"

// ReplaceEventsDeliveryForTest swaps the outbound event transport for offline
// integration tests only.
func (s *Service) ReplaceEventsDeliveryForTest(d *dotevents.Delivery) {
	if d == nil {
		d = dotevents.NewDelivery(nil)
	}
	a, ok := s.events.(eventsAdapter)
	if !ok {
		panic("events adapter unavailable")
	}
	a.Manager.SetDeliveryForTest(d)
}
