package codexbridge

import (
	"github.com/dotcontrol/dotcontrol/internal/connections"
	"github.com/dotcontrol/dotcontrol/internal/model"
)

func (b *Bridge) ProfileCatalog() *model.CodexProfileCatalog {
	b.mu.Lock()
	dir := ""
	if b.runtime != nil {
		dir = b.runtime.cfg.ConnectionsDir
	}
	b.mu.Unlock()
	if dir == "" {
		return nil
	}
	cfg, err := connections.NewStore(dir).Load()
	if err != nil {
		return nil
	}
	out := &model.CodexProfileCatalog{Execution: []model.CodexProfileSummary{}}
	for _, p := range cfg.ExecutionProfiles {
		out.Execution = append(out.Execution, model.CodexProfileSummary{ID: p.ID, Name: p.Name, Execution: p.Execution, Presentation: p.Presentation})
	}
	return out
}
