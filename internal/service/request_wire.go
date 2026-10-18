package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

type requestWire Request
type compatibilityRequest struct {
	requestWire
	// Older gateways reject this unknown field, preventing a retired request
	// from reaching an older implementation that could still execute it.
	RetiredCodexRequest bool `json:"retired_codex_request,omitempty"`
}

func (r Request) MarshalJSON() ([]byte, error) {
	retired := r.Codex != nil && (r.Codex.RetiredFieldsPresent || r.Codex.ServerProfileID != "" || r.Codex.ExperimentID != "")
	return json.Marshal(compatibilityRequest{requestWire(r), retired})
}

func (r *Request) UnmarshalJSON(data []byte) error {
	var wire compatibilityRequest
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if dec.Decode(&wire) != nil || dec.Decode(new(any)) != io.EOF {
		return errors.New("invalid_request")
	}
	if wire.RetiredCodexRequest && wire.Codex != nil {
		wire.Codex.RetiredFieldsPresent = true
	}
	*r = Request(wire.requestWire)
	return nil
}
