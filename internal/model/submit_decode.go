package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// Preserve the historical wire shape and hash while detecting even empty
// retired fields on new requests. Stored job records keep their original types.
func (r *CodexSubmit) UnmarshalJSON(data []byte) error {
	type wire CodexSubmit
	var value wire
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&value); err != nil {
		return errors.New("invalid_request")
	}
	if dec.Decode(new(any)) != io.EOF {
		return errors.New("invalid_request")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return errors.New("invalid_request")
	}
	for name := range fields {
		if strings.EqualFold(name, "server_profile_id") || strings.EqualFold(name, "experiment_id") {
			value.RetiredFieldsPresent = true
		}
	}
	*r = CodexSubmit(value)
	return nil
}
