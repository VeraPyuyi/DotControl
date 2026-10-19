package dotbridge

import "net/http"

// JobListDTO contains retained job metadata in newest-first order. Results are
// omitted to bound the list response; GET /dot-bridge/v1/jobs/{id} returns the
// full result. The queue retains at most 100 jobs across all clients.
type JobListDTO struct {
	Version int      `json:"version"`
	Jobs    []JobDTO `json:"jobs"`
}

func (s *Service) listJobs(client ClientPolicy) (JobListDTO, error) {
	if err := s.clientGate(client.ClientID); err != nil {
		return JobListDTO{}, err
	}
	jobs, err := s.queue.List(client.ClientID)
	// Recheck even when queue I/O fails so an intervening gate change wins.
	if gateErr := s.clientGate(client.ClientID); gateErr != nil {
		return JobListDTO{}, gateErr
	}
	if err != nil {
		return JobListDTO{}, err
	}
	out := JobListDTO{Version: APIVersion, Jobs: make([]JobDTO, 0, len(jobs))}
	for _, job := range jobs {
		job.Result = nil
		out.Jobs = append(out.Jobs, toJobDTO(job))
	}
	return out, nil
}

func (s *Service) handleListJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.listJobs(clientFrom(r.Context()))
	if err != nil {
		writeError(w, http.StatusBadRequest, mapError(err), "")
		return
	}
	writeJSON(w, http.StatusOK, jobs)
}
