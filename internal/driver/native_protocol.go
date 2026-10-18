package driver

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

const protocolLimit = 8192

// Only named error containers and literal enum values are inspected. Messages,
// URLs, paths, arbitrary keys and response bodies never become log fields.
type protocolMetadata struct {
	code, stage, class                      string
	codeFormat, stageFormat, classFormat    string
	codeField, stageField, classField       string
	numeric                                 *int
	exitCode                                *int
	executionState                          string
	codePresent, stagePresent, classPresent bool
	conflict, truncated                     bool
	nodes                                   int
	source                                  string
}

func boundedExitCode(value any) (int, bool) {
	var n int64
	switch v := value.(type) {
	case int:
		n = int64(v)
	case int32:
		n = int64(v)
	case int64:
		n = v
	case json.Number:
		if len(v) > 4 {
			return 0, false
		}
		var err error
		n, err = v.Int64()
		if err != nil {
			return 0, false
		}
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) || v != math.Trunc(v) || v < -1 || v > 255 {
			return 0, false
		}
		n = int64(v)
	default:
		return 0, false
	}
	return int(n), n >= -1 && n <= 255
}

// Shape and field names come from fixed enums, never from the response value.
// Unknown identifiers, numbers outside allowed ranges, and object contents
// remain hidden. This distinguishes an unsupported format from an unknown code.
func metadataFormat(value any) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case string:
		if len(v) > 90 {
			return "string_oversize"
		}
		if v == "" {
			return "string_empty"
		}
		if canonicalInteger(v) {
			return "string_numeric"
		}
		lower, upper, identifier, underscore := true, true, true, false
		for i, c := range []byte(v) {
			if c == '_' {
				underscore = true
				continue
			}
			if c >= '0' && c <= '9' && i > 0 {
				continue
			}
			if c < 'a' || c > 'z' {
				lower = false
			}
			if c < 'A' || c > 'Z' {
				upper = false
			}
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
				identifier = false
			}
		}
		if lower && underscore {
			return "string_snake_case"
		}
		if upper && underscore {
			return "string_upper_snake_case"
		}
		if identifier {
			return "string_identifier"
		}
		return "string_other"
	case int, int32, int64, json.Number, float64:
		return "number"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case bool:
		return "boolean"
	default:
		return "other"
	}
}

func canonicalInteger(s string) bool {
	if len(s) == 0 || len(s) > 20 {
		return false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return err == nil && strconv.FormatInt(n, 10) == s
}

func selectShape(destination *string, value string) {
	if *destination == "" {
		*destination = value
	} else if *destination != value {
		*destination = "mixed"
	}
}

func (m *protocolMetadata) shape(format, field *string, key string, value any) {
	selectShape(format, metadataFormat(value))
	selectShape(field, key)
}

func (m *protocolMetadata) selectEnum(destination *string, value any, allowed map[string]bool) {
	s, ok := value.(string)
	if !ok || len(s) > 90 || !allowed[s] {
		if ok && len(s) > 90 {
			m.truncated = true
		}
		return
	}
	if *destination != "" && *destination != s {
		m.conflict = true
		return
	}
	*destination = s
}

func numericCode(value any) (int, bool) {
	var n int64
	switch v := value.(type) {
	case string:
		// Compatibility for a canonical integer string; no trim, exponent,
		// leading zero, arbitrary digits, or symbolic text is accepted.
		if !canonicalInteger(v) {
			return 0, false
		}
		n, _ = strconv.ParseInt(v, 10, 64)
	case int:
		n = int64(v)
	case int32:
		n = int64(v)
	case int64:
		n = v
	case json.Number:
		if len(v) > 20 {
			return 0, false
		}
		var err error
		n, err = v.Int64()
		if err != nil {
			return 0, false
		}
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) || v != math.Trunc(v) || v < -32700 || v > 106 {
			return 0, false
		}
		n = int64(v)
	default:
		return 0, false
	}
	// JSON-RPC, macOS AX errors and the bounded Darwin errno range only.
	ok := n == -32700 || (n >= -32603 && n <= -32600) || (n >= -32099 && n <= -32000) || (n >= -25214 && n <= -25200) || (n >= 1 && n <= 106)
	return int(n), ok
}

func decodeErrorJSON(text string) any {
	if len(text) == 0 || len(text) > protocolLimit {
		return nil
	}
	if !uniqueJSONKeys(text) {
		return nil
	}
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return nil
	}
	// Reject trailing data, including a second object or private plain text.
	var trailing any
	if decoder.Decode(&trailing) == nil {
		return nil
	}
	if bytes.TrimSpace([]byte(text))[0] != '{' {
		return nil
	}
	var check json.RawMessage
	if json.Unmarshal([]byte(text), &check) != nil {
		return nil
	}
	return value
}

// Reject duplicate keys instead of allowing a later secret/code to overwrite
// earlier evidence. This validation is bounded and does not retain key names.
func uniqueJSONKeys(text string) bool {
	d := json.NewDecoder(strings.NewReader(text))
	d.UseNumber()
	budget := 512
	var visit func(int) bool
	visit = func(depth int) bool {
		budget--
		if depth > 16 || budget < 0 {
			return false
		}
		token, err := d.Token()
		if err != nil {
			return false
		}
		delim, container := token.(json.Delim)
		if !container {
			return true
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				name, ok := key.(string)
				if err != nil || !ok || seen[name] {
					return false
				}
				seen[name] = true
				if !visit(depth + 1) {
					return false
				}
			}
			end, err := d.Token()
			return err == nil && end == json.Delim('}')
		case '[':
			for d.More() {
				if !visit(depth + 1) {
					return false
				}
			}
			end, err := d.Token()
			return err == nil && end == json.Delim(']')
		default:
			return false
		}
	}
	return visit(0)
}

func (m *protocolMetadata) read(value any, depth int, source string) {
	if depth > 4 || m.nodes >= 12 {
		m.truncated = true
		return
	}
	var fields map[string]any
	switch v := value.(type) {
	case map[string]any:
		fields = v
	case json.RawMessage:
		if len(v) > protocolLimit {
			m.truncated = true
			return
		}
		fields, _ = decodeErrorJSON(string(v)).(map[string]any)
	case string:
		if len(v) > protocolLimit {
			m.truncated = true
			return
		}
		fields, _ = decodeErrorJSON(v).(map[string]any)
	}
	if fields == nil {
		return
	}
	m.nodes++
	present := false
	if v, ok := fields["exit_code"]; ok {
		present = true
		if n, valid := boundedExitCode(v); valid {
			if m.exitCode != nil && *m.exitCode != n {
				m.conflict = true
			} else {
				m.exitCode = &n
			}
		}
	}
	if v, ok := fields["execution_state"]; ok {
		present = true
		state := "unclassified"
		if s, ok := v.(string); ok && s == "unknown" {
			state = "unknown"
		}
		if m.executionState != "" && m.executionState != state {
			m.conflict = true
		} else {
			m.executionState = state
		}
	}
	for _, key := range []string{"error_code", "errorCode", "code"} {
		if v, ok := fields[key]; ok {
			present, m.codePresent = true, true
			m.shape(&m.codeFormat, &m.codeField, key, v)
			m.selectEnum(&m.code, v, driverCodes)
			if n, ok := numericCode(v); ok {
				if m.numeric != nil && *m.numeric != n {
					m.conflict = true
				} else {
					m.numeric = &n
				}
			}
		}
	}
	for _, key := range []string{"error_stage", "errorStage", "stage", "phase"} {
		if v, ok := fields[key]; ok {
			present, m.stagePresent = true, true
			m.shape(&m.stageFormat, &m.stageField, key, v)
			m.selectEnum(&m.stage, v, driverStages)
		}
	}
	for _, key := range []string{"error_class", "errorClass", "class", "category", "error_kind", "kind"} {
		if v, ok := fields[key]; ok {
			present, m.classPresent = true, true
			m.shape(&m.classFormat, &m.classField, key, v)
			m.selectEnum(&m.class, v, driverClasses)
		}
	}
	if present {
		if m.source == "" {
			m.source = source
		} else if m.source != source {
			m.source = "mixed"
		}
	}
	// 0.34.1 SDK explicitly reads refusal.code as well as top-level code.
	// Never visit invalid_output, apps or arbitrary response containers.
	for _, key := range []string{"error", "refusal", "details", "cause", "data"} {
		if nested, ok := fields[key]; ok {
			m.read(nested, depth+1, source)
		}
	}
}
