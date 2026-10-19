package mcpserver

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
)

// DiscoverEventsWrapper only augments a successful modern SDK discovery result.
// Other traffic remains on the original SDK ResponseWriter (including streaming).
func DiscoverEventsWrapper(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("MCP-Protocol-Version") != "2026-07-28" || r.Header.Get("Mcp-Method") != "server/discover" {
			next.ServeHTTP(w, r)
			return
		}
		rec := &responseRecorder{header: make(http.Header)}
		next.ServeHTTP(rec, r)
		body := rec.body.Bytes()
		if rec.status == 200 {
			body = patchDiscoverEvents(body)
		}
		for k, vals := range rec.header {
			for _, v := range vals {
				w.Header().Add(k, v)
			}
		}
		w.Header().Del("Content-Length")
		status := rec.status
		if status == 0 {
			status = 200
		}
		w.WriteHeader(status)
		_, _ = io.Copy(w, bytes.NewReader(body))
	})
}

type responseRecorder struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (r *responseRecorder) Header() http.Header { return r.header }
func (r *responseRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.WriteHeader(200)
	}
	return r.body.Write(b)
}
func (r *responseRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
}
func patchDiscoverEvents(body []byte) []byte {
	var env map[string]json.RawMessage
	if json.Unmarshal(body, &env) != nil || env["error"] != nil || env["id"] == nil {
		return body
	}
	var typed struct {
		SupportedVersions []string         `json:"supportedVersions"`
		Capabilities      *json.RawMessage `json:"capabilities"`
	}
	if json.Unmarshal(env["result"], &typed) != nil || typed.Capabilities == nil {
		return body
	}
	modern := false
	for _, v := range typed.SupportedVersions {
		if v == "2026-07-28" {
			modern = true
		}
	}
	if !modern {
		return body
	}
	var result, caps map[string]json.RawMessage
	if json.Unmarshal(env["result"], &result) != nil || json.Unmarshal(*typed.Capabilities, &caps) != nil || caps == nil {
		return body
	}
	caps["events"] = json.RawMessage(`{}`)
	result["capabilities"], _ = json.Marshal(caps)
	env["result"], _ = json.Marshal(result)
	out, e := json.Marshal(env)
	if e != nil {
		return body
	}
	return out
}
