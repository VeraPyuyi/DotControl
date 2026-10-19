package mcpserver

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
)

// DiscoverEventsWrapper augments successful 2026-07-28 server/discover responses
// with events capability without replacing SDK dispatch.
func DiscoverEventsWrapper(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}
		rec := &responseRecorder{header: make(http.Header)}
		next.ServeHTTP(rec, r)
		body := rec.body.Bytes()
		if rec.status == http.StatusOK && bytes.Contains(body, []byte(`"method":"server/discover"`)) {
			body = patchDiscoverEvents(body)
		}
		for k, vals := range rec.header {
			for _, v := range vals {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(rec.status)
		_, _ = io.Copy(w, bytes.NewReader(body))
	})
}

type responseRecorder struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (r *responseRecorder) Header() http.Header { return r.header }
func (r *responseRecorder) Write(b []byte) (int, error) { return r.body.Write(b) }
func (r *responseRecorder) WriteHeader(statusCode int) { r.status = statusCode }

func patchDiscoverEvents(body []byte) []byte {
	var env map[string]json.RawMessage
	if json.Unmarshal(body, &env) != nil {
		return body
	}
	result, ok := env["result"]
	if !ok {
		return body
	}
	var obj map[string]any
	if json.Unmarshal(result, &obj) != nil {
		return body
	}
	caps, ok := obj["capabilities"].(map[string]any)
	if !ok {
		return body
	}
	if _, exists := caps["events"]; !exists {
		caps["events"] = map[string]any{}
		obj["capabilities"] = caps
		patched, err := json.Marshal(obj)
		if err != nil {
			return body
		}
		env["result"] = patched
		out, err := json.Marshal(env)
		if err != nil {
			return body
		}
		return out
	}
	return body
}
