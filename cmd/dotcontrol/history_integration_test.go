package main

import (
	"context"
	"github.com/dotcontrol/dotcontrol/internal/codexhistory"
	"github.com/dotcontrol/dotcontrol/internal/config"
	"github.com/dotcontrol/dotcontrol/internal/engine"
	"github.com/dotcontrol/dotcontrol/internal/historyaccess"
	"github.com/dotcontrol/dotcontrol/internal/model"
	"os"
	"testing"
)

// Explicit opt-in: reads only the chosen real local thread IDs. Output contains
// counts/codes, never chat text, titles, paths or credentials. Policies use temp.
func TestOfficialHistoryReadOnlyAcceptance(t *testing.T) {
	id, other := os.Getenv("DOTCONTROL_HISTORY_TEST_THREAD"), os.Getenv("DOTCONTROL_HISTORY_EXCLUDED_THREAD")
	if id == "" || other == "" {
		t.Skip("explicit local history acceptance IDs not supplied")
	}
	dir := t.TempDir()
	c := config.New("history-local-acceptance", "")
	c.HistoryEnabled = true
	c.TerminalOnly = true
	eng, e := engine.New(engine.Config{DeviceID: c.DeviceID, Name: c.Name, StateDir: dir}, unavailableDriver{}, testHistoryMonitor{})
	if e != nil {
		t.Fatal(e)
	}
	defer eng.Close()
	eng.Pause(false)
	owner := &runtimeOwner{dir: dir, cfg: c, eng: eng}
	owner.history = owner.newHistory()
	store := historyaccess.NewStore(dir)
	p, e := store.InitializeAll()
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	if e = owner.history.Check(ctx); e != nil {
		t.Fatal(e)
	}
	list, e := owner.history.Threads(ctx, codexhistory.ListRequest{Limit: 1})
	if e != nil || len(list.Threads) != 1 {
		t.Fatalf("list count=%d error=%v", len(list.Threads), e)
	}
	result, e := owner.history.Read(ctx, codexhistory.ReadRequest{ThreadID: id, Limit: 1})
	if e != nil || len(result.Turns) != 1 || len(result.Turns[0].Messages) == 0 {
		t.Fatalf("read turns=%d error=%v", len(result.Turns), e)
	}
	if result.NextCursor != "" {
		older, e := owner.history.Read(ctx, codexhistory.ReadRequest{ThreadID: id, Limit: 1, Cursor: result.NextCursor})
		if e != nil || len(older.Turns) != 1 {
			t.Fatal("older page failed", e)
		}
	}
	p, e = store.Apply(p.Revision, "allowlist", []string{id})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = owner.history.Read(ctx, codexhistory.ReadRequest{ThreadID: id}); e != nil {
		t.Fatal("permitted failed", e)
	}
	if _, e = owner.history.Read(ctx, codexhistory.ReadRequest{ThreadID: other}); e == nil || e.Error() != "history_thread_not_shared" {
		t.Fatal("excluded failed", e)
	}
	if list.NextCursor != "" {
		if _, e = owner.history.Threads(ctx, codexhistory.ListRequest{Limit: 1, Cursor: list.NextCursor}); e == nil {
			t.Fatal("old cursor survived")
		}
	}
	restricted, e := owner.history.Threads(ctx, codexhistory.ListRequest{Limit: 100})
	if e != nil {
		t.Fatal(e)
	}
	for _, row := range restricted.Threads {
		if row.ID != id {
			t.Fatal("unauthorized metadata")
		}
	}
	p, e = store.Apply(p.Revision, "allowlist", []string{})
	if e != nil {
		t.Fatal(e)
	}
	empty, e := owner.history.Projects(ctx, codexhistory.ListRequest{})
	if e != nil || len(empty.Projects) != 0 {
		t.Fatal("empty projects", e)
	}
	t.Log("official local read, older paging, allowlist direct denial, filtered listing, old cursor invalidation and empty projects passed; no turns started")
}

type testHistoryMonitor struct{}

func (testHistoryMonitor) Snapshot() model.Activity { return model.Activity{Available: false} }
