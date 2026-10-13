package main

import (
	"context"
	"encoding/json"
	"github.com/dotcontrol/dotcontrol/internal/codexhistory"
	"github.com/dotcontrol/dotcontrol/internal/config"
	"github.com/dotcontrol/dotcontrol/internal/historyaccess"
	"github.com/dotcontrol/dotcontrol/internal/model"
	"github.com/dotcontrol/dotcontrol/internal/onboard"
	"io"
	"testing"
	"time"
)

type historyRPC func(string, any) (any, error)

func (f historyRPC) Call(_ context.Context, m string, p any, out any) error {
	v, e := f(m, p)
	if e != nil {
		return e
	}
	b, _ := json.Marshal(v)
	return json.Unmarshal(b, out)
}

type historyCloser struct{}

func (historyCloser) Close() error { return nil }
func TestLocalPreviewRequiresApplyAndRevision(t *testing.T) {
	const id = "11111111-1111-4111-8111-111111111111"
	dir := t.TempDir()
	store := historyaccess.NewStore(dir)
	original, e := store.InitializeAll()
	if e != nil {
		t.Fatal(e)
	}
	o := &runtimeOwner{dir: dir, cfg: config.New("test", "")}
	o.history = codexhistory.New(store, func(context.Context) (codexhistory.RPC, io.Closer, error) {
		return historyRPC(func(m string, p any) (any, error) {
			if m != "thread/read" {
				t.Fatal(m)
			}
			return map[string]any{"thread": map[string]any{"id": id, "name": "picked"}}, nil
		}), historyCloser{}, nil
	}, func() codexhistory.Access { return codexhistory.Access{Enabled: true} })
	raw, e := o.History(context.Background(), onboard.HistoryRequest{Operation: "preview", Revision: original.Revision, Mode: "allowlist", ThreadIDs: []string{id}})
	if e != nil {
		t.Fatal(e)
	}
	pr := raw.(historyPreview)
	if time.Until(pr.Expires) < 55*time.Minute {
		t.Fatal("scope preview expires during normal setup")
	}
	p, _ := store.Load()
	if p.Mode != "all" {
		t.Fatal("preview mutated permissions")
	}
	_, e = o.History(context.Background(), onboard.HistoryRequest{Operation: "apply", PreviewID: pr.ID, Revision: pr.Revision + 1})
	if e == nil {
		t.Fatal("wrong revision accepted")
	}
	_, e = o.History(context.Background(), onboard.HistoryRequest{Operation: "apply", PreviewID: pr.ID, Revision: pr.Revision})
	if e != nil {
		t.Fatal(e)
	}
	p, _ = store.Load()
	if p.Mode != "allowlist" || len(p.AllowedThreadIDs) != 1 {
		t.Fatal(p)
	}
	if _, e = o.History(context.Background(), onboard.HistoryRequest{Operation: "apply", PreviewID: pr.ID, Revision: pr.Revision}); e == nil {
		t.Fatal("replay accepted")
	}
	raw, e = o.History(context.Background(), onboard.HistoryRequest{Operation: "preview", Revision: p.Revision, Mode: "all"})
	if e != nil {
		t.Fatal(e)
	}
	pr = raw.(historyPreview)
	pr.Expires = time.Now().Add(-time.Second)
	o.historyPreviews[pr.ID] = pr
	if _, e = o.History(context.Background(), onboard.HistoryRequest{Operation: "apply", PreviewID: pr.ID, Revision: pr.Revision}); e == nil {
		t.Fatal("expired preview")
	}
}
func TestOldConfigDoesNotEnableHistoryAndHistoryResumeNeedsNoDesktop(t *testing.T) {
	c := config.New("test", "")
	if c.HistoryEnabled {
		t.Fatal("upgrade enabled history")
	}
	c.HistoryEnabled = true
	if e := resumeReadiness(c, false, false, permissionStatus{}, model.Activity{}); e != nil {
		t.Fatal(e)
	}
	if e := resumeReadiness(c, true, false, permissionStatus{}, model.Activity{}); e == nil {
		t.Fatal("revoked accepted")
	}
}

func TestDisablePreemptsSlowEnable(t *testing.T) {
	dir := t.TempDir()
	started, finish := make(chan struct{}), make(chan struct{})
	o := &runtimeOwner{dir: dir, cfg: config.New("test", "")}
	o.cfg.HistoryEnabled = true
	o.history = codexhistory.New(historyaccess.NewStore(dir), func(context.Context) (codexhistory.RPC, io.Closer, error) {
		return historyRPC(func(string, any) (any, error) { close(started); <-finish; return map[string]any{"data": []any{}}, nil }), historyCloser{}, nil
	}, func() codexhistory.Access { return codexhistory.Access{Enabled: true} })
	done := make(chan error, 1)
	go func() {
		_, e := o.History(context.Background(), onboard.HistoryRequest{Operation: "enable"})
		done <- e
	}()
	<-started
	disabled := make(chan error, 1)
	go func() {
		_, e := o.History(context.Background(), onboard.HistoryRequest{Operation: "disable"})
		disabled <- e
	}()
	select {
	case e := <-disabled:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		close(finish)
		t.Fatal("disable waited for upstream")
	}
	close(finish)
	if e := <-done; e == nil || e.Error() != "history_request_superseded" {
		t.Fatal(e)
	}
	if o.cfg.HistoryEnabled {
		t.Fatal("late enable restored sharing")
	}
}
