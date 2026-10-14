package onboard

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// Retired local request fields must not silently become absent when their JSON
// values are empty or null. This decoder does not apply to persisted profiles.
func (r *ConnectionsRequest) UnmarshalJSON(data []byte) error {
	type wire ConnectionsRequest
	var value wire
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF {
		return errors.New("invalid_input")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return errors.New("invalid_input")
	}
	for field := range fields {
		for _, retired := range []string{"server", "server_profile_id", "public_key_path", "authorization_id"} {
			if strings.EqualFold(field, retired) {
				value.RetiredFieldsPresent = true
			}
		}
	}
	*r = ConnectionsRequest(value)
	return nil
}
