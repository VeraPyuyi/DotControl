package dotbridge

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

const maxBody = 128 << 10

func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/dot-bridge/v1/status", s.handleStatus)
	mux.HandleFunc("/dot-bridge/v1/jobs", s.handleJobs)
	mux.HandleFunc("/dot-bridge/v1/jobs/", s.handleJobByID)
	mux.HandleFunc("/dot-bridge/v1/requests/", s.handleRequestByID)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		ip, err := netip.ParseAddr(host)
		if err != nil || !ip.IsLoopback() || r.Header.Get("Origin") != "" {
			writeError(w, http.StatusForbidden, "invalid_origin", "")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method_rejected", "")
			return
		}
		client, err := s.authenticate(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "")
			return
		}
		r = r.WithContext(withClient(r.Context(), client))
		mux.ServeHTTP(w, r)
	})
}

func (s *Service) authenticate(r *http.Request) (ClientPolicy, error) {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return ClientPolicy{}, errors.New("unauthorized")
	}
	return s.clientFromToken(strings.TrimSpace(strings.TrimPrefix(auth, "Bearer ")))
}

func (s *Service) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_rejected", "")
		return
	}
	client := clientFrom(r.Context())
	status, err := s.StatusForClient(client.ClientID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bridge_unavailable", "")
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Service) handleJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_rejected", "")
		return
	}
	client := clientFrom(r.Context())
	req, err := decodeSubmit(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "")
		return
	}
	job, code, err := s.submit(client, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, mapError(err), job.JobID)
		return
	}
	writeJSON(w, code, job)
}

func (s *Service) handleJobByID(w http.ResponseWriter, r *http.Request) {
	client := clientFrom(r.Context())
	jobID := strings.TrimPrefix(r.URL.Path, "/dot-bridge/v1/jobs/")
	cancelRoute := strings.HasSuffix(jobID, "/cancel")
	if cancelRoute {
		jobID = strings.TrimSuffix(jobID, "/cancel")
	}
	if jobID == "" || strings.Contains(jobID, "/") {
		writeError(w, http.StatusNotFound, "notfound", "")
		return
	}
	if cancelRoute {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method_rejected", "")
			return
		}
		jobID = strings.TrimSuffix(jobID, "/cancel")
		job, err := s.cancelJob(client, jobID)
		if err != nil {
			writeError(w, http.StatusBadRequest, mapError(err), jobID)
			return
		}
		writeJSON(w, http.StatusOK, job)
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_rejected", "")
		return
	}
	job, err := s.getJob(client, jobID)
	if err != nil {
		writeError(w, http.StatusNotFound, mapError(err), jobID)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Service) handleRequestByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_rejected", "")
		return
	}
	client := clientFrom(r.Context())
	requestID := strings.TrimPrefix(r.URL.Path, "/dot-bridge/v1/requests/")
	if requestID == "" || strings.Contains(requestID, "/") {
		writeError(w, http.StatusNotFound, "notfound", "")
		return
	}
	job, err := s.byRequest(client, requestID)
	if err != nil {
		writeError(w, http.StatusNotFound, mapError(err), "")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func decodeSubmit(body io.Reader) (SubmitRequest, error) {
	dec := json.NewDecoder(io.LimitReader(body, maxBody))
	dec.DisallowUnknownFields()
	var req SubmitRequest
	if dec.Decode(&req) != nil {
		return SubmitRequest{}, errors.New("invalid_request")
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		return SubmitRequest{}, errors.New("invalid_request")
	}
	return req, nil
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, errCode, jobID string) {
	writeJSON(w, code, ErrorDTO{Error: ErrorBody{Code: errCode, JobID: jobID}})
}

func mapError(err error) string {
	if err == nil {
		return "unknown"
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "probe_required"):
		return "probe_required"
	case strings.Contains(msg, "bridge_disabled"):
		return "bridge_disabled"
	case strings.Contains(msg, "bridge_paused"):
		return "bridge_paused"
	case strings.Contains(msg, "access_revoked"):
		return "access_revoked"
	case strings.Contains(msg, "notfound"):
		return "notfound"
	case strings.Contains(msg, "conflict"):
		return "conflict"
	default:
		return "unknown"
	}
}
