package dotqueue

// CapacityStatus reports global retained job counts for operator visibility.
// It intentionally omits client-identifying fields.
type CapacityStatus struct {
	Limit     int `json:"limit"`
	Retained  int `json:"retained"`
	Remaining int `json:"remaining"`
}

func (s *Store) Capacity() (CapacityStatus, error) {
	var out CapacityStatus
	err := s.withLock(false, false, func(st *persistenceState) error {
		if !st.Enabled {
			return errf("disabled")
		}
		retained := len(st.Jobs)
		out = CapacityStatus{
			Limit:     maxRetainedJobs,
			Retained:  retained,
			Remaining: maxRetainedJobs - retained,
		}
		return nil
	})
	return out, err
}
