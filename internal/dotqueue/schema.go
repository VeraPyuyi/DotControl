package dotqueue

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"time"
)

// strictShape validates required fields, nullability and exact nested schemas
// before encoding/json can turn missing/null scalars into usable zero values.
func strictShape(raw []byte, t reflect.Type) error {
	if t.Kind() == reflect.Pointer {
		if bytes.Equal(raw, []byte("null")) {
			return errf("corrupt")
		}
		return strictShape(raw, t.Elem())
	}
	if bytes.Equal(raw, []byte("null")) {
		return errf("corrupt")
	}
	if t == reflect.TypeOf(time.Time{}) {
		var v time.Time
		if json.Unmarshal(raw, &v) != nil {
			return errf("corrupt")
		}
		return nil
	}
	switch t.Kind() {
	case reflect.Struct:
		var m map[string]json.RawMessage
		if json.Unmarshal(raw, &m) != nil || m == nil {
			return errf("corrupt")
		}
		fields := map[string]reflect.StructField{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			tag := strings.Split(f.Tag.Get("json"), ",")
			fields[tag[0]] = f
			if _, ok := m[tag[0]]; !ok && !strings.Contains(f.Tag.Get("json"), "omitempty") {
				return errf("corrupt")
			}
		}
		for k, v := range m {
			f, ok := fields[k]
			if !ok || strictShape(v, f.Type) != nil {
				return errf("corrupt")
			}
		}
	case reflect.Map:
		var m map[string]json.RawMessage
		if json.Unmarshal(raw, &m) != nil || m == nil {
			return errf("corrupt")
		}
		for _, v := range m {
			if strictShape(v, t.Elem()) != nil {
				return errf("corrupt")
			}
		}
	case reflect.Slice:
		// Nil message/artifact lists are canonicalized to [] before persistence.
		var a []json.RawMessage
		if json.Unmarshal(raw, &a) != nil || a == nil {
			return errf("corrupt")
		}
		for _, v := range a {
			if strictShape(v, t.Elem()) != nil {
				return errf("corrupt")
			}
		}
	default:
		v := reflect.New(t).Interface()
		if json.Unmarshal(raw, v) != nil {
			return errf("corrupt")
		}
	}
	return nil
}
