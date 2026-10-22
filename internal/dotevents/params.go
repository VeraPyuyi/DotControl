package dotevents

import (
	"bytes"
	"encoding/json"
	"io"
	"unicode/utf8"
)

// UnmarshalJSON rejects ambiguous duplicate keys and unknown fields before the
// integration adapter can bind the authenticated principal.
func (p *Params) UnmarshalJSON(data []byte) error {
	if len(data) > maxParamsBytes || !utf8.Valid(data) {
		return errf("unknown")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	if e := uniqueJSON(d, 0); e != nil {
		return e
	}
	if _, e := d.Token(); e != io.EOF {
		return errf("unknown")
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(data, &obj) != nil || obj == nil {
		return errf("unknown")
	}
	for k, v := range obj {
		switch k {
		case "name", "arguments", "delivery", "cursor", "ttlMs":
		default:
			return errf("unknown")
		}
		if k != "cursor" && k != "ttlMs" && bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return errf("unknown")
		}
	}
	for _, k := range []string{"name", "arguments", "delivery"} {
		if _, ok := obj[k]; !ok {
			return errf("unknown")
		}
	}
	for k, fields := range map[string][]string{"arguments": {"queue_id"}, "delivery": {"mode", "url", "secret"}} {
		var nested map[string]json.RawMessage
		if json.Unmarshal(obj[k], &nested) != nil || nested == nil {
			return errf("unknown")
		}
		for key, v := range nested {
			allowed := false
			for _, f := range fields {
				if f == key {
					allowed = true
				}
			}
			if !allowed || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
				return errf("unknown")
			}
		}
	}
	type plain Params
	var out plain
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&out) != nil {
		return errf("unknown")
	}
	*p = Params(out)
	return nil
}
func uniqueJSON(d *json.Decoder, depth int) error {
	if depth > 16 {
		return errf("unknown")
	}
	tok, e := d.Token()
	if e != nil {
		return errf("unknown")
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return errf("unknown")
			}
			s, ok := k.(string)
			if !ok || seen[s] {
				return errf("unknown")
			}
			seen[s] = true
			if e = uniqueJSON(d, depth+1); e != nil {
				return e
			}
		}
	case '[':
		for d.More() {
			if e = uniqueJSON(d, depth+1); e != nil {
				return e
			}
		}
	default:
		return errf("unknown")
	}
	end, e := d.Token()
	if e != nil || (delim == '{' && end != json.Delim('}')) || (delim == '[' && end != json.Delim(']')) {
		return errf("unknown")
	}
	return nil
}
