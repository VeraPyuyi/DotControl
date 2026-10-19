package dotqueue

func (s *Store) List(clientID string) ([]Job, error) {
	if err := validateID(clientID); err != nil {
		return nil, err
	}
	out := make([]Job, 0)
	err := s.withLock(false, false, func(st *persistenceState) error {
		if !st.Enabled {
			return errf("disabled")
		}
		for i := len(st.Order) - 1; i >= 0; i-- {
			id := st.Order[i]
			rec := st.Jobs[id]
			if rec == nil || rec.ClientID != clientID {
				continue
			}
			out = append(out, copyPublicJob(rec))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func copyPublicJob(rec *jobRecord) Job {
	job := publicJob(rec)
	if rec.Result != nil {
		job.Result = &Result{
			Text:      rec.Result.Text,
			Artifacts: append([]Artifact(nil), rec.Result.Artifacts...),
			ErrorCode: rec.Result.ErrorCode,
			Cancelled: rec.Result.Cancelled,
		}
	}
	return job
}
