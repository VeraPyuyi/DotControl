package historyaccess

// The metadata identity, availability, and fingerprint approach is adapted
// from sidebar-organizer (MIT). See third_party/sidebar-organizer.md.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"unicode"
)

const maxSnapshotBytes = 8 * 1024 * 1024

type Selection struct {
	SectionIDs []string `json:"section_ids"`
	ProjectIDs []string `json:"project_ids"`
	ThreadIDs  []string `json:"thread_ids"`
}

type Candidates struct {
	ThreadIDs    []string `json:"thread_ids"`
	SnapshotHash string   `json:"snapshot_hash"`
	Unresolved   []Issue  `json:"unresolved"`
	Complete     bool     `json:"complete"`
}

type Issue struct {
	Code string `json:"code"`
	ID   string `json:"id,omitempty"`
}

type threadRow struct{ id, kind, host, project string }
type projectRow struct{ id, kind, host string }
type sidebarIndex struct {
	threads          map[string]threadRow
	projects         map[string]projectRow
	sections         map[string][]string
	identities       map[string]bool
	references       map[string]string
	projectConflicts map[string]bool
	memberships      map[string]map[string]bool
	blocked          bool
	result           Candidates
	issues           map[Issue]bool
	selected         map[string]bool
}

func stableMetadataID(s string) bool {
	return s != "" && len(s) <= 4096 && strings.TrimSpace(s) == s && !strings.ContainsFunc(s, unicode.IsControl)
}

func object(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var result map[string]json.RawMessage
	if json.Unmarshal(raw, &result) != nil || result == nil {
		return nil, failure("invalid_snapshot")
	}
	return result, nil
}

func array(obj map[string]json.RawMessage, key string) ([]json.RawMessage, error) {
	var result []json.RawMessage
	if json.Unmarshal(obj[key], &result) != nil || result == nil {
		return nil, failure("invalid_snapshot")
	}
	return result, nil
}

// Optional metadata strings can be absent/null. Identity eligibility below
// still requires an explicit local host and Codex kind.
func metadataString(obj map[string]json.RawMessage, key string) (string, error) {
	raw, ok := obj[key]
	if !ok || bytes.Equal(raw, []byte("null")) {
		return "", nil
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return "", failure("invalid_snapshot")
	}
	return value, nil
}

func fields(raw json.RawMessage, keys ...string) ([]string, error) {
	obj, err := object(raw)
	if err != nil {
		return nil, err
	}
	values := make([]string, len(keys))
	for i, key := range keys {
		values[i], err = metadataString(obj, key)
		if err != nil {
			return nil, err
		}
	}
	return values, nil
}

func (idx *sidebarIndex) issue(code, id string) {
	// Unsafe or unbounded strings never become error/report payloads.
	if !stableMetadataID(id) {
		id = ""
	}
	idx.issues[Issue{Code: code, ID: id}] = true
}

func availabilityBlocked(rows []json.RawMessage, field, wanted string) bool {
	for _, raw := range rows {
		var id string
		if json.Unmarshal(raw, &id) != nil {
			obj, err := object(raw)
			if err != nil {
				return true
			}
			id, err = metadataString(obj, field)
			if err != nil {
				return true
			}
		}
		if !stableMetadataID(id) || id == "*" || id == wanted {
			return true
		}
	}
	return false
}

func isDefaultSection(id string) bool {
	switch id {
	case "threads", "chats", "projects", "orbit", "agents":
		return true
	}
	return false
}

func (idx *sidebarIndex) addMembership(key, section string) {
	if isDefaultSection(section) {
		return
	}
	if idx.memberships[key] == nil {
		idx.memberships[key] = map[string]bool{}
	}
	idx.memberships[key][section] = true
}

func (idx *sidebarIndex) noteReference(key string) {
	var id, route string
	if rest, ok := strings.CutPrefix(key, "codex:thread:"); ok {
		host, thread, found := strings.Cut(rest, ":")
		if found {
			id, route = thread, "codex:thread:"+host
		}
	} else if thread, ok := strings.CutPrefix(key, "chatgpt:conversation:"); ok {
		id, route = thread, "chatgpt:conversation"
	}
	if !ValidateThreadID(id) {
		return
	}
	if previous, exists := idx.references[id]; exists && previous != route {
		idx.identities[id] = true
		idx.issue("conflicting_identity", id)
	}
	idx.references[id] = route
}

func (idx *sidebarIndex) indexSections(rows []json.RawMessage) error {
	for _, raw := range rows {
		obj, err := object(raw)
		if err != nil {
			return err
		}
		sid, err := metadataString(obj, "sectionId")
		if err != nil {
			return err
		}
		name, err := metadataString(obj, "name")
		if err != nil {
			return err
		}
		if !stableMetadataID(sid) || !stableMetadataID(name) {
			return failure("invalid_snapshot")
		}
		if _, exists := idx.sections[sid]; exists {
			return failure("invalid_snapshot")
		}
		keys, err := array(obj, "itemKeys")
		if err != nil {
			return err
		}
		idx.sections[sid] = []string{}
		for _, rawKey := range keys {
			var key string
			if json.Unmarshal(rawKey, &key) != nil || !stableMetadataID(key) {
				return failure("invalid_snapshot")
			}
			idx.sections[sid] = append(idx.sections[sid], key)
			idx.addMembership(key, sid)
			idx.noteReference(key)
		}
	}
	return nil
}

func (idx *sidebarIndex) indexProjects(rows []json.RawMessage) error {
	for _, raw := range rows {
		f, err := fields(raw, "projectId", "projectKind", "hostId")
		if err != nil {
			return err
		}
		p := projectRow{f[0], f[1], f[2]}
		if !stableMetadataID(p.id) {
			idx.issue("invalid_project_id", "")
			continue
		}
		if previous, ok := idx.projects[p.id]; ok && previous != p {
			idx.projectConflicts[p.id] = true
			idx.issue("conflicting_project", p.id)
		} else {
			idx.projects[p.id] = p
		}
		if p.kind != "local" || p.host != "local" {
			idx.issue("unsupported_project", p.id)
		}
	}
	return nil
}

func (idx *sidebarIndex) indexThreads(rows []json.RawMessage, pinned bool) error {
	for _, raw := range rows {
		f, err := fields(raw, "id", "kind", "hostId", "projectId")
		if err != nil {
			return err
		}
		t := threadRow{f[0], f[1], f[2], f[3]}
		if !ValidateThreadID(t.id) {
			idx.issue("invalid_thread_id", t.id)
			continue
		}
		route := t.kind + ":thread:" + t.host
		if t.kind == "chatgpt" {
			route = "chatgpt:conversation"
		}
		if reference, exists := idx.references[t.id]; exists && reference != route {
			idx.identities[t.id] = true
			idx.issue("conflicting_identity", t.id)
		}
		if previous, ok := idx.threads[t.id]; ok && previous != t {
			idx.identities[t.id] = true
			idx.issue("conflicting_identity", t.id)
		} else {
			idx.threads[t.id] = t
		}
		if t.kind != "codex" || t.host != "local" {
			idx.issue("unsupported_identity", t.id)
		}
		if pinned && t.kind == "codex" && t.host == "local" {
			idx.addMembership("codex:thread:local:"+t.id, "pinned")
		}
	}
	return nil
}

func (idx *sidebarIndex) addThread(id string) {
	if !ValidateThreadID(id) {
		idx.issue("invalid_thread_id", id)
		return
	}
	t, exists := idx.threads[id]
	if !exists {
		idx.issue("missing_thread", id)
		return
	}
	if idx.identities[id] {
		idx.issue("conflicting_identity", id)
		return
	}
	if t.kind != "codex" || t.host != "local" {
		idx.issue("unsupported_identity", id)
		return
	}
	if idx.blocked {
		idx.issue("source_unavailable", id)
		return
	}
	if len(idx.memberships["codex:thread:local:"+id]) > 1 {
		idx.issue("conflicting_membership", id)
		return
	}
	if idx.projectConflicts[t.project] {
		idx.issue("conflicting_project", t.project)
		return
	}
	idx.selected[id] = true
}

func (idx *sidebarIndex) addProject(id string) {
	if !stableMetadataID(id) {
		idx.issue("invalid_project_id", "")
		return
	}
	p, exists := idx.projects[id]
	if !exists {
		idx.issue("missing_project", id)
		return
	}
	if idx.projectConflicts[id] {
		idx.issue("conflicting_project", id)
		return
	}
	if p.kind != "local" || p.host != "local" {
		idx.issue("unsupported_project", id)
		return
	}
	if idx.blocked {
		idx.issue("source_unavailable", id)
		return
	}
	if len(idx.memberships["codex:project:"+id]) > 1 {
		idx.issue("conflicting_membership", id)
		return
	}
	idx.issue("incomplete_project_membership", id)
	for threadID, t := range idx.threads {
		if t.project == id {
			idx.addThread(threadID)
		}
	}
}

func (idx *sidebarIndex) addItem(key string) {
	if id, ok := strings.CutPrefix(key, "codex:project:"); ok {
		idx.addProject(id)
		return
	}
	if rest, ok := strings.CutPrefix(key, "codex:thread:"); ok {
		host, id, ok := strings.Cut(rest, ":")
		if !ok {
			idx.issue("invalid_item_key", key)
			return
		}
		if host != "local" {
			idx.issue("unsupported_identity", key)
			return
		}
		idx.addThread(id)
		return
	}
	idx.issue("unsupported_item", key)
}

// ResolveSidebar consumes only native list_threads/list_projects metadata.
// Candidates are never consent. Each ID still needs official verification and
// explicit local human review before it is stored in an allowlist. Native
// recent lists never establish completeness, even for selected projects.
func ResolveSidebar(raw []byte, selection Selection) (Candidates, error) {
	obj, err := decodeObject(raw, maxSnapshotBytes)
	if err != nil || len(obj) != 2 {
		return Candidates{}, failure("invalid_snapshot")
	}
	projects, err := object(obj["projects"])
	if err != nil {
		return Candidates{}, err
	}
	sidebar, err := object(obj["sidebar"])
	if err != nil {
		return Candidates{}, err
	}
	projectRows, err := array(projects, "projects")
	if err != nil {
		return Candidates{}, err
	}
	lists := map[string][]json.RawMessage{}
	for _, name := range []string{"threads", "pinnedThreads", "sections", "unavailableHosts", "unavailableSources"} {
		lists[name], err = array(sidebar, name)
		if err != nil {
			return Candidates{}, err
		}
	}
	if len(selection.ThreadIDs)+len(selection.ProjectIDs)+len(selection.SectionIDs) > maxThreadIDs {
		return Candidates{}, failure("invalid_selection")
	}
	for _, list := range [][]string{selection.SectionIDs, selection.ProjectIDs} {
		for _, id := range list {
			if !stableMetadataID(id) {
				return Candidates{}, failure("invalid_selection")
			}
		}
	}
	// Canonical JSON object ordering ignores whitespace without discarding any
	// native snapshot fields. Numeric lexemes remain lossless through UseNumber.
	var canonical any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	_ = decoder.Decode(&canonical)
	canonicalBytes, _ := json.Marshal(canonical)
	sum := sha256.Sum256(canonicalBytes)
	idx := sidebarIndex{threads: map[string]threadRow{}, projects: map[string]projectRow{}, sections: map[string][]string{}, identities: map[string]bool{}, references: map[string]string{}, projectConflicts: map[string]bool{}, memberships: map[string]map[string]bool{}, issues: map[Issue]bool{}, selected: map[string]bool{}, result: Candidates{ThreadIDs: []string{}, SnapshotHash: hex.EncodeToString(sum[:]), Unresolved: []Issue{}, Complete: false}}
	idx.blocked = availabilityBlocked(lists["unavailableHosts"], "hostId", "local") || availabilityBlocked(lists["unavailableSources"], "source", "codex")
	if idx.blocked {
		idx.issue("source_unavailable", "")
	}
	if err = idx.indexSections(lists["sections"]); err != nil {
		return Candidates{}, err
	}
	if err = idx.indexProjects(projectRows); err != nil {
		return Candidates{}, err
	}
	if err = idx.indexThreads(lists["threads"], false); err != nil {
		return Candidates{}, err
	}
	if err = idx.indexThreads(lists["pinnedThreads"], true); err != nil {
		return Candidates{}, err
	}
	for _, id := range selection.ThreadIDs {
		idx.addThread(id)
	}
	for _, id := range selection.ProjectIDs {
		idx.addProject(id)
	}
	for _, id := range selection.SectionIDs {
		keys, exists := idx.sections[id]
		if !exists {
			idx.issue("missing_section", id)
			continue
		}
		for _, key := range keys {
			idx.addItem(key)
		}
	}
	for id := range idx.selected {
		idx.result.ThreadIDs = append(idx.result.ThreadIDs, id)
	}
	slices.Sort(idx.result.ThreadIDs)
	for issue := range idx.issues {
		idx.result.Unresolved = append(idx.result.Unresolved, issue)
	}
	slices.SortFunc(idx.result.Unresolved, func(a, b Issue) int {
		if n := strings.Compare(a.Code, b.Code); n != 0 {
			return n
		}
		return strings.Compare(a.ID, b.ID)
	})
	return idx.result, nil
}
