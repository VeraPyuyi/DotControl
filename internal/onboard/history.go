package onboard

import (
	"context"
	"encoding/json"
	"github.com/dotcontrol/dotcontrol/internal/codexhistory"
	"github.com/dotcontrol/dotcontrol/internal/historyaccess"
)

// HistoryRequest is local-admin/UI only. No MCP tool accepts this structure.
type HistoryRequest struct {
	Operation string                   `json:"operation"`
	Revision  uint64                   `json:"revision,omitempty"`
	Mode      string                   `json:"mode,omitempty"`
	ThreadIDs []string                 `json:"thread_ids,omitempty"`
	Snapshot  json.RawMessage          `json:"snapshot,omitempty"`
	Selection historyaccess.Selection  `json:"selection,omitempty"`
	Query     codexhistory.ListRequest `json:"query,omitempty"`
	PreviewID string                   `json:"preview_id,omitempty"`
}
type HistoryBackend interface {
	History(context.Context, HistoryRequest) (any, error)
}
