package historyaccess

import (
	"encoding/json"
	"reflect"
	"testing"
)

func fixtureSnapshot() map[string]any {
	return map[string]any{
		"projects": map[string]any{"projects": []any{map[string]any{"projectId": "project-local", "projectKind": "local", "hostId": "local", "name": "Same"}}},
		"sidebar": map[string]any{
			"threads":          []any{map[string]any{"id": idA, "kind": "codex", "hostId": "local", "projectId": "project-local"}, map[string]any{"id": idB, "kind": "codex", "hostId": "local", "projectId": nil}},
			"pinnedThreads":    []any{},
			"sections":         []any{map[string]any{"sectionId": "codex:section:one", "name": "Same", "itemKeys": []any{"codex:project:project-local"}}, map[string]any{"sectionId": "chatgpt:section:one", "name": "Same", "itemKeys": []any{"codex:thread:local:" + idB}}},
			"unavailableHosts": []any{}, "unavailableSources": []any{},
		},
	}
}

func resolveFixture(t *testing.T, raw map[string]any, selection Selection) Candidates {
	t.Helper()
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	before := string(b)
	got, err := ResolveSidebar(b, selection)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(raw)
	if string(after) != before {
		t.Fatal("resolver mutated input")
	}
	if got.Complete || got.SnapshotHash == "" || got.ThreadIDs == nil || got.Unresolved == nil {
		t.Fatalf("invalid envelope: %+v", got)
	}
	return got
}

func hasIssue(got Candidates, code, id string) bool {
	for _, issue := range got.Unresolved {
		if issue.Code == code && issue.ID == id {
			return true
		}
	}
	return false
}

func TestSidebarExactSectionIDsAndPartialProjects(t *testing.T) {
	s := fixtureSnapshot()
	got := resolveFixture(t, s, Selection{SectionIDs: []string{"codex:section:one"}})
	if !reflect.DeepEqual(got.ThreadIDs, []string{idA}) || !hasIssue(got, "incomplete_project_membership", "project-local") {
		t.Fatalf("project expansion: %+v", got)
	}
	got = resolveFixture(t, s, Selection{SectionIDs: []string{"chatgpt:section:one"}})
	if !reflect.DeepEqual(got.ThreadIDs, []string{idB}) {
		t.Fatalf("same names merged: %+v", got)
	}
	got = resolveFixture(t, s, Selection{SectionIDs: []string{"Same"}})
	if len(got.ThreadIDs) != 0 || !hasIssue(got, "missing_section", "Same") {
		t.Fatalf("guessed section by name: %+v", got)
	}
	got = resolveFixture(t, s, Selection{ProjectIDs: []string{"project-local"}, ThreadIDs: []string{idB, idA, idB}})
	if !reflect.DeepEqual(got.ThreadIDs, []string{idA, idB}) {
		t.Fatalf("dedup/sort: %+v", got)
	}
}

func TestSidebarRejectsNonlocalAndTemporaryIdentities(t *testing.T) {
	for _, tc := range []struct{ kind, host, id string }{{"codex", "durable", idA}, {"codex", "cloud", idA}, {"codex", "remote", idA}, {"chatgpt", "local", idA}, {"codex", "", idA}, {"codex", "local", "client-new-thread:" + idA}, {"codex", "local", "../" + idA}} {
		t.Run(tc.kind+tc.host+tc.id, func(t *testing.T) {
			s := fixtureSnapshot()
			sidebar := s["sidebar"].(map[string]any)
			sidebar["threads"] = []any{map[string]any{"id": tc.id, "kind": tc.kind, "hostId": tc.host}}
			got := resolveFixture(t, s, Selection{ThreadIDs: []string{tc.id}})
			if len(got.ThreadIDs) != 0 || len(got.Unresolved) == 0 {
				t.Fatalf("unsupported row accepted: %+v", got)
			}
		})
	}
}

func TestSidebarRepeatedStablePinIsAllowedButConflictsAreSkipped(t *testing.T) {
	s := fixtureSnapshot()
	side := s["sidebar"].(map[string]any)
	side["pinnedThreads"] = []any{map[string]any{"id": idA, "kind": "codex", "hostId": "local", "projectId": "project-local", "title": "different title is irrelevant"}}
	got := resolveFixture(t, s, Selection{ThreadIDs: []string{idA}})
	if !reflect.DeepEqual(got.ThreadIDs, []string{idA}) {
		t.Fatalf("consistent recent and pin blocked: %+v", got)
	}
	for _, conflict := range []map[string]any{{"id": idA, "kind": "codex", "hostId": "durable", "projectId": "project-local"}, {"id": idA, "kind": "codex", "hostId": "local", "projectId": "different-project"}} {
		side["pinnedThreads"] = []any{conflict}
		got = resolveFixture(t, s, Selection{ThreadIDs: []string{idA}})
		if len(got.ThreadIDs) != 0 || !hasIssue(got, "conflicting_identity", idA) {
			t.Fatalf("conflict accepted: %+v", got)
		}
	}
}

func TestSidebarMissingAndConflictingMembers(t *testing.T) {
	s := fixtureSnapshot()
	side := s["sidebar"].(map[string]any)
	side["sections"] = []any{map[string]any{"sectionId": "one", "name": "One", "itemKeys": []any{"codex:thread:local:" + idA, "codex:thread:local:" + idB}}, map[string]any{"sectionId": "two", "name": "Two", "itemKeys": []any{"codex:thread:local:" + idA}}}
	got := resolveFixture(t, s, Selection{SectionIDs: []string{"one"}})
	if !reflect.DeepEqual(got.ThreadIDs, []string{idB}) || !hasIssue(got, "conflicting_membership", idA) {
		t.Fatalf("conflicting membership: %+v", got)
	}
	side["threads"] = []any{}
	got = resolveFixture(t, s, Selection{SectionIDs: []string{"one"}})
	if len(got.ThreadIDs) != 0 || !hasIssue(got, "missing_thread", idB) {
		t.Fatalf("unlisted member guessed: %+v", got)
	}
	got = resolveFixture(t, s, Selection{ProjectIDs: []string{"project-local"}})
	if len(got.ThreadIDs) != 0 || !hasIssue(got, "incomplete_project_membership", "project-local") {
		t.Fatalf("empty project marked complete: %+v", got)
	}
}

func TestSidebarUnavailableAndAmbiguousProject(t *testing.T) {
	for _, tc := range []struct {
		field string
		value any
	}{{"unavailableHosts", []any{"local"}}, {"unavailableHosts", []any{map[string]any{"hostId": "local"}}}, {"unavailableSources", []any{"codex"}}, {"unavailableSources", []any{map[string]any{"unexpected": "unknown"}}}} {
		s := fixtureSnapshot()
		s["sidebar"].(map[string]any)[tc.field] = tc.value
		got := resolveFixture(t, s, Selection{ThreadIDs: []string{idA}})
		if len(got.ThreadIDs) != 0 {
			t.Fatalf("unavailable source accepted: %+v", got)
		}
	}
	s := fixtureSnapshot()
	s["projects"].(map[string]any)["projects"] = []any{map[string]any{"projectId": "project-local", "projectKind": "local", "hostId": "local"}, map[string]any{"projectId": "project-local", "projectKind": "chatgpt", "hostId": "local"}}
	got := resolveFixture(t, s, Selection{ProjectIDs: []string{"project-local"}})
	if len(got.ThreadIDs) != 0 || !hasIssue(got, "conflicting_project", "project-local") {
		t.Fatalf("ambiguous project expanded: %+v", got)
	}
}

func TestSidebarStrictShapeAndCanonicalHash(t *testing.T) {
	s := fixtureSnapshot()
	b, _ := json.Marshal(s)
	for _, bad := range [][]byte{[]byte("null"), []byte("{}"), append(append([]byte{}, b...), b...), []byte(`{"projects":{},"projects":{},"sidebar":{}}`)} {
		_, err := ResolveSidebar(bad, Selection{})
		wantCode(t, err, "history_invalid_snapshot")
	}
	for _, field := range []string{"threads", "pinnedThreads", "sections", "unavailableHosts", "unavailableSources"} {
		s := fixtureSnapshot()
		s["sidebar"].(map[string]any)[field] = nil
		b, _ := json.Marshal(s)
		_, err := ResolveSidebar(b, Selection{})
		wantCode(t, err, "history_invalid_snapshot")
	}
	got := resolveFixture(t, s, Selection{ThreadIDs: []string{idA}})
	pretty, _ := json.MarshalIndent(s, "", "  ")
	gotPretty, err := ResolveSidebar(pretty, Selection{ThreadIDs: []string{idA}})
	if err != nil || !reflect.DeepEqual(got, gotPretty) {
		t.Fatalf("hash changed with whitespace: %+v %v", gotPretty, err)
	}
	s["sidebar"].(map[string]any)["threads"].([]any)[0].(map[string]any)["projectId"] = "changed"
	changed := resolveFixture(t, s, Selection{ThreadIDs: []string{idA}})
	if changed.SnapshotHash == got.SnapshotHash {
		t.Fatal("hash omitted membership")
	}
}

func TestSidebarCrossNamespaceReferencesDoNotAuthorize(t *testing.T) {
	s := fixtureSnapshot()
	s["sidebar"].(map[string]any)["sections"] = []any{
		map[string]any{"sectionId": "local", "name": "Same", "itemKeys": []any{"codex:thread:local:" + idB}},
		map[string]any{"sectionId": "remote", "name": "Same", "itemKeys": []any{"codex:thread:durable:" + idB}},
	}
	got := resolveFixture(t, s, Selection{SectionIDs: []string{"local"}})
	if len(got.ThreadIDs) != 0 || !hasIssue(got, "conflicting_identity", idB) {
		t.Fatalf("cross-host reference conflict accepted: %+v", got)
	}
}
