package codexhistory

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/historyaccess"
)

const permitted = "11111111-1111-4111-8111-111111111111"
const excluded = "22222222-2222-4222-8222-222222222222"

type fakeRPC func(string, any) (any, error)

func (f fakeRPC) Call(_ context.Context, m string, p any, out any) error {
	v, e := f(m, p)
	if e != nil {
		return e
	}
	b, _ := json.Marshal(v)
	return json.Unmarshal(b, out)
}

func TestProcessConcurrencyBoundAndEmptyAllowlistDoesNotProbe(t *testing.T) {
	s, store := fixture(t, func(string, any) (any, error) { t.Fatal("empty policy probed upstream"); return nil, nil })
	p, _ := store.Load()
	store.Apply(p.Revision, "allowlist", []string{})
	v, e := s.Threads(context.Background(), ListRequest{Search: "private", Limit: 1})
	if e != nil || len(v.Threads) != 0 || v.NextCursor != "" {
		t.Fatal(v, e)
	}
	_, one, e := s.connect(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer one.Close()
	_, two, e := s.connect(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer two.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, _, e = s.connect(ctx); e == nil {
		t.Fatal("more than two readers")
	}
	one.Close()
	if _, three, e := s.connect(context.Background()); e != nil {
		t.Fatal(e)
	} else {
		three.Close()
	}
}

type closer struct{}

func (closer) Close() error { return nil }
func fixture(t *testing.T, f fakeRPC) (*Service, *historyaccess.Store) {
	t.Helper()
	store := historyaccess.NewStore(t.TempDir())
	p, e := store.InitializeAll()
	if e != nil {
		t.Fatal(e)
	}
	_, e = store.Apply(p.Revision, "allowlist", []string{permitted})
	if e != nil {
		t.Fatal(e)
	}
	s := New(store, func(context.Context) (RPC, io.Closer, error) { return f, closer{}, nil }, func() Access { return Access{Enabled: true} })
	return s, store
}
func TestFilterBeforeReturningAndDenyDirectRead(t *testing.T) {
	s, _ := fixture(t, func(m string, p any) (any, error) {
		if m != "thread/list" {
			t.Fatalf("unexpected read %s", m)
		}
		return map[string]any{"data": []any{map[string]any{"id": excluded, "name": "SECRET"}, map[string]any{"id": permitted, "name": "allowed"}}}, nil
	})
	v, e := s.Threads(context.Background(), ListRequest{})
	if e != nil || len(v.Threads) != 1 || v.Threads[0].Title != "allowed" {
		t.Fatalf("%+v %v", v, e)
	}
	_, e = s.Read(context.Background(), ReadRequest{ThreadID: excluded})
	if e == nil || e.Error() != "history_thread_not_shared" {
		t.Fatal(e)
	}
}
func TestReadProjectionAndInflightPolicyChange(t *testing.T) {
	var store *historyaccess.Store
	change := false
	s, st := fixture(t, func(m string, p any) (any, error) {
		switch m {
		case "thread/read":
			return map[string]any{"thread": map[string]any{"id": permitted, "name": "allowed"}}, nil
		case "thread/turns/list":
			if change {
				old, _ := store.Load()
				_, e := store.Apply(old.Revision, "allowlist", []string{})
				if e != nil {
					t.Fatal(e)
				}
			}
			return map[string]any{"data": []any{map[string]any{"id": "turn1", "items": []any{map[string]any{"type": "userMessage", "content": []any{map[string]any{"type": "text", "text": "hi"}, map[string]any{"type": "image", "url": "SECRET"}}}, map[string]any{"type": "reasoning", "text": "SECRET"}, map[string]any{"type": "commandExecution", "text": "SECRET"}, map[string]any{"type": "agentMessage", "text": "hello"}}}}}, nil
		}
		t.Fatalf("mutation or unknown method %s", m)
		return nil, nil
	})
	store = st
	v, e := s.Read(context.Background(), ReadRequest{ThreadID: permitted})
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(v)
	if strings.Contains(string(b), "SECRET") || len(v.Turns) != 1 || len(v.Turns[0].Messages) != 2 || !v.Turns[0].OmittedNonText {
		t.Fatal(string(b))
	}
	change = true
	if _, e = s.Read(context.Background(), ReadRequest{ThreadID: permitted}); e == nil || e.Error() != "history_access_changed" {
		t.Fatal(e)
	}
}
func TestCursorBoundToPolicyAndFilters(t *testing.T) {
	s, store := fixture(t, func(m string, p any) (any, error) {
		return map[string]any{"data": []any{map[string]any{"id": permitted, "name": "allowed"}}, "nextCursor": "private-upstream"}, nil
	})
	v, e := s.Threads(context.Background(), ListRequest{Limit: 1})
	if e != nil || v.NextCursor == "" {
		t.Fatal(e)
	}
	if strings.Contains(v.NextCursor, "private-upstream") {
		t.Fatal("raw cursor")
	}
	if _, e = s.Threads(context.Background(), ListRequest{Limit: 1, Search: "other", Cursor: v.NextCursor}); e == nil {
		t.Fatal("changed filters accepted")
	}
	p, _ := store.Load()
	store.Apply(p.Revision, "all", []string{})
	if _, e = s.Threads(context.Background(), ListRequest{Limit: 1, Cursor: v.NextCursor}); e == nil {
		t.Fatal("old policy cursor accepted")
	}
}
func TestPauseAndRevocationBlockWithoutDriver(t *testing.T) {
	s, _ := fixture(t, func(string, any) (any, error) { t.Fatal("must not call"); return nil, nil })
	for _, a := range []Access{{Enabled: false}, {Enabled: true, Paused: true}, {Enabled: true, Revoked: true}} {
		s.access = func() Access { return a }
		if _, e := s.Threads(context.Background(), ListRequest{}); e == nil {
			t.Fatal(a)
		}
	}
}

func TestProjectsDoNotExposeExcludedMembership(t *testing.T) {
	s, _ := fixture(t, func(m string, p any) (any, error) {
		switch m {
		case "thread/read":
			return map[string]any{"thread": map[string]any{"id": permitted, "projectId": "allowed-project"}}, nil
		case "project/list":
			return map[string]any{"data": []any{map[string]any{"id": "private-project", "name": "SECRET"}, map[string]any{"id": "allowed-project", "name": "shared"}}}, nil
		}
		t.Fatal(m)
		return nil, nil
	})
	v, e := s.Projects(context.Background(), ListRequest{})
	if e != nil || len(v.Projects) != 1 || v.Projects[0].Name != "shared" {
		t.Fatalf("%+v %v", v, e)
	}
}
func TestFilteredPaginationContinuesAcrossExcludedPages(t *testing.T) {
	calls := 0
	s, _ := fixture(t, func(m string, p any) (any, error) {
		calls++
		if calls == 1 {
			return map[string]any{"data": []any{map[string]any{"id": excluded, "name": "SECRET"}}, "nextCursor": "p2"}, nil
		}
		if p.(map[string]any)["cursor"] != "p2" {
			t.Fatal(p)
		}
		return map[string]any{"data": []any{map[string]any{"id": permitted, "name": "shared"}}}, nil
	})
	v, e := s.Threads(context.Background(), ListRequest{Limit: 1})
	if e != nil || calls != 2 || len(v.Threads) != 1 || v.Threads[0].ID != permitted {
		t.Fatalf("%+v %v calls=%d", v, e, calls)
	}
}
func TestInflightPauseSuppressesReadAndRestartInvalidatesCursor(t *testing.T) {
	paused := false
	s, store := fixture(t, func(m string, p any) (any, error) {
		paused = true
		return map[string]any{"data": []any{map[string]any{"id": permitted, "name": "shared"}}, "nextCursor": "private"}, nil
	})
	s.access = func() Access { return Access{Enabled: true, Paused: paused} }
	if _, e := s.Threads(context.Background(), ListRequest{Limit: 1}); e == nil || e.Error() != "device_paused" {
		t.Fatal(e)
	}
	p, _ := store.Load()
	cursor := s.encode("private", "threads", p, queryHash(ListRequest{Limit: 1}))
	other := New(store, s.open, func() Access { return Access{Enabled: true} })
	if _, e := other.decode(cursor, "threads", p, queryHash(ListRequest{Limit: 1})); e == nil {
		t.Fatal("restart key accepted")
	}
}
