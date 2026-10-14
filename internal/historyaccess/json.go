package historyaccess

import (
	"bytes"
	"encoding/json"
	"io"
	"unicode/utf8"
)

// decodeObject rejects duplicate object keys (encoding/json otherwise accepts
// the last value), invalid UTF-8, trailing JSON, and excessive nesting.
func decodeObject(data []byte, limit int) (map[string]json.RawMessage, error) {
	if len(data) > limit || !utf8.Valid(data) {
		return nil, failure("invalid_json")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := jsonValue(d, 0); err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, failure("invalid_json")
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(data, &obj) != nil || obj == nil {
		return nil, failure("invalid_json")
	}
	return obj, nil
}

func jsonValue(d *json.Decoder, depth int) error {
	if depth > 64 {
		return failure("invalid_json")
	}
	t, err := d.Token()
	if err != nil {
		return failure("invalid_json")
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return failure("invalid_json")
			}
			k, ok := key.(string)
			if !ok || seen[k] {
				return failure("invalid_json")
			}
			seen[k] = true
			if err := jsonValue(d, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := jsonValue(d, depth+1); err != nil {
				return err
			}
		}
	default:
		return failure("invalid_json")
	}
	end, err := d.Token()
	if err != nil || (delim == '{' && end != json.Delim('}')) || (delim == '[' && end != json.Delim(']')) {
		return failure("invalid_json")
	}
	return nil
}
