package dotqueue

import (
	"bytes"
	"encoding/json"
	"io"
	"unicode/utf8"
)

func decodeObject(data []byte, limit int) (map[string]json.RawMessage, error) {
	if len(data) > limit || !utf8.Valid(data) {
		return nil, errf("corrupt")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := jsonValue(d, 0); err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errf("corrupt")
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(data, &obj) != nil || obj == nil {
		return nil, errf("corrupt")
	}
	return obj, nil
}

func jsonValue(d *json.Decoder, depth int) error {
	if depth > 64 {
		return errf("corrupt")
	}
	t, err := d.Token()
	if err != nil {
		return errf("corrupt")
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
				return errf("corrupt")
			}
			k, ok := key.(string)
			if !ok || seen[k] {
				return errf("corrupt")
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
		return errf("corrupt")
	}
	end, err := d.Token()
	if err != nil || (delim == '{' && end != json.Delim('}')) || (delim == '[' && end != json.Delim(']')) {
		return errf("corrupt")
	}
	return nil
}
