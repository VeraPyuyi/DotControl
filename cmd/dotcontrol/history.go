package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/dotcontrol/dotcontrol/internal/codexbridge"
	"github.com/dotcontrol/dotcontrol/internal/codexhistory"
	"github.com/dotcontrol/dotcontrol/internal/config"
	"github.com/dotcontrol/dotcontrol/internal/connections"
	"github.com/dotcontrol/dotcontrol/internal/historyaccess"
	"github.com/dotcontrol/dotcontrol/internal/localapi"
	"github.com/dotcontrol/dotcontrol/internal/model"
	"github.com/dotcontrol/dotcontrol/internal/onboard"
	"github.com/dotcontrol/dotcontrol/internal/vault"
)

type historyPreview struct {
	ID              string                `json:"preview_id"`
	Revision        uint64                `json:"revision"`
	Mode            string                `json:"mode"`
	Threads         []codexhistory.Thread `json:"threads"`
	Added           []string              `json:"added"`
	Removed         []string              `json:"removed"`
	Issues          []historyaccess.Issue `json:"unresolved"`
	Coverage        string                `json:"coverage"`
	Expires         time.Time             `json:"expires"`
	BaseFingerprint string                `json:"-"`
	SnapshotHash    string                `json:"snapshot_hash,omitempty"`
}

func (o *runtimeOwner) newHistory() *codexhistory.Service {
	return codexhistory.New(historyaccess.NewStore(o.dir), func(ctx context.Context) (codexhistory.RPC, io.Closer, error) {
		saved, e := connections.NewStore(filepath.Join(o.dir, "connections")).Load()
		if e != nil {
			return nil, nil, e
		}
		found, e := connections.DiscoverCodex(ctx, saved.CodexExecutable, connections.DiscoveryOptions{})
		if e != nil || !found.Available {
			return nil, nil, errors.New("history_codex_unavailable")
		}
		if found.Version != codexbridge.SupportedAppServerVersion {
			return nil, nil, errors.New("history_codex_version_unsupported")
		}
		client, e := codexbridge.Start(ctx, found.Executable, o.dir)
		if e != nil {
			return nil, nil, e
		}
		var init any
		e = client.Call(ctx, "initialize", map[string]any{"clientInfo": map[string]any{"name": "dotcontrol_history", "version": model.Version}, "capabilities": map[string]any{"experimentalApi": true}}, &init)
		if e == nil {
			e = client.Notify(ctx, "initialized", map[string]any{})
		}
		if e != nil {
			client.Close()
			return nil, nil, e
		}
		return client, client, nil
	}, func() codexhistory.Access {
		o.mu.Lock()
		defer o.mu.Unlock()
		return codexhistory.Access{Enabled: o.cfg.HistoryEnabled, Revoked: o.revoked, Paused: o.eng.Device().Paused}
	})
}
func (o *runtimeOwner) History(ctx context.Context, r onboard.HistoryRequest) (any, error) {
	o.mu.Lock()
	epoch := o.historyEpoch
	o.mu.Unlock()
	// Stop-sharing must not queue behind metadata queries or a slow preview.
	if r.Operation != "disable" && r.Operation != "status" && r.Operation != "catalog" && r.Operation != "check" {
		o.historyMu.Lock()
		defer o.historyMu.Unlock()
	}
	store := historyaccess.NewStore(o.dir)
	switch r.Operation {
	case "status":
		p, e := store.Load()
		code := ""
		if e != nil {
			code = e.Error()
		}
		return map[string]any{"capability": o.history.Capability(), "policy": p, "policy_error": code}, nil
	case "enable":
		if e := o.history.Check(ctx); e != nil {
			return nil, e
		}
		p, e := store.InitializeAll()
		if e != nil {
			return nil, e
		}
		o.mu.Lock()
		defer o.mu.Unlock()
		if o.revoked {
			return nil, errors.New("device_revoked")
		}
		if epoch != o.historyEpoch {
			return nil, errors.New("history_request_superseded")
		}
		next := o.cfg
		next.HistoryEnabled = true
		if e = config.Save(o.dir, next); e != nil {
			return nil, e
		}
		o.cfg = next
		return p, nil
	case "disable":
		o.mu.Lock()
		defer o.mu.Unlock()
		o.historyEpoch++
		next := o.cfg
		next.HistoryEnabled = false
		if e := config.Save(o.dir, next); e != nil {
			return nil, e
		}
		o.cfg = next
		return map[string]bool{"enabled": false}, nil
	case "check":
		return map[string]bool{"checked": true}, o.history.Check(ctx)
	case "catalog":
		return o.history.Catalog(ctx, r.Query)
	case "preview":
		p, e := store.Load()
		if e != nil {
			return nil, e
		}
		if r.Revision != p.Revision {
			return nil, errors.New("history_revision_conflict")
		}
		if r.Mode != "all" && r.Mode != "allowlist" {
			return nil, errors.New("history_invalid_mode")
		}
		preview := historyPreview{ID: config.Random(), Revision: p.Revision, Mode: r.Mode, Threads: []codexhistory.Thread{}, Added: []string{}, Removed: []string{}, Issues: []historyaccess.Issue{}, Coverage: "explicit_selection_only", Expires: time.Now().Add(15 * time.Minute), BaseFingerprint: historyaccess.Fingerprint(p)}
		ids := r.ThreadIDs
		if len(r.Snapshot) > 0 {
			candidates, e := historyaccess.ResolveSidebar(r.Snapshot, r.Selection)
			if e != nil {
				return nil, e
			}
			ids = append(ids, candidates.ThreadIDs...)
			preview.SnapshotHash = candidates.SnapshotHash
			preview.Issues = candidates.Unresolved
			preview.Coverage = "bounded_sidebar_snapshot_not_complete_history"
		}
		if r.Mode == "all" {
			if len(ids) > 0 || len(r.Snapshot) > 0 {
				return nil, errors.New("history_all_has_selection")
			}
			preview.Coverage = "all_local_threads_including_future"
		} else {
			preview.Threads, e = o.history.Verify(ctx, ids)
			if e != nil {
				return nil, e
			}
		}
		old := map[string]bool{}
		newIDs := map[string]bool{}
		for _, id := range p.AllowedThreadIDs {
			old[id] = true
		}
		for _, t := range preview.Threads {
			newIDs[t.ID] = true
			if p.Mode == "allowlist" && r.Mode == "allowlist" && !old[t.ID] {
				preview.Added = append(preview.Added, t.ID)
			}
		}
		for id := range old {
			if p.Mode == "allowlist" && r.Mode == "allowlist" && !newIDs[id] {
				preview.Removed = append(preview.Removed, id)
			}
		}
		sort.Strings(preview.Added)
		sort.Strings(preview.Removed)
		if o.historyPreviews == nil {
			o.historyPreviews = map[string]historyPreview{}
		}
		for id, pr := range o.historyPreviews {
			if time.Now().After(pr.Expires) {
				delete(o.historyPreviews, id)
			}
		}
		if len(o.historyPreviews) >= 16 {
			return nil, errors.New("history_too_many_previews")
		}
		o.historyPreviews[preview.ID] = preview
		return preview, nil
	case "previews":
		out := []historyPreview{}
		for id, p := range o.historyPreviews {
			if time.Now().After(p.Expires) {
				delete(o.historyPreviews, id)
			} else {
				out = append(out, p)
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Expires.Before(out[j].Expires) })
		return out, nil
	case "apply":
		pr, ok := o.historyPreviews[r.PreviewID]
		if !ok || time.Now().After(pr.Expires) {
			return nil, errors.New("history_preview_expired")
		}
		p, e := store.Load()
		if e != nil {
			return nil, e
		}
		if p.Revision != r.Revision || pr.Revision != r.Revision || historyaccess.Fingerprint(p) != pr.BaseFingerprint {
			return nil, errors.New("history_revision_conflict")
		}
		ids := codexhistory.SortedIDs(pr.Threads)
		if pr.Mode == "allowlist" {
			if _, e = o.history.Verify(ctx, ids); e != nil {
				return nil, e
			}
		}
		p, e = store.Apply(r.Revision, pr.Mode, ids)
		if e != nil {
			return nil, e
		}
		o.historyPreviews = map[string]historyPreview{}
		return p, nil
	default:
		return nil, errors.New("history_unknown_operation")
	}
}

// Local CLI shares the same authenticated engine as the protected wizard.
// Preview never grants access. Apply requires the reviewed preview's revision.
func historyCLI(ctx context.Context, dir string, args []string) error {
	f := flags("history")
	file := f.String("file", "", "Local selection JSON for preview")
	preview := f.String("preview-id", "", "Reviewed preview ID")
	rev := f.Uint64("revision", 0, "Expected policy revision")
	if len(args) == 0 {
		return errors.New("history_requires_status_enable_disable_catalog_preview_apply_all")
	}
	op := args[0]
	if e := f.Parse(args[1:]); e != nil {
		return e
	}
	r := onboard.HistoryRequest{Operation: op, Revision: *rev, PreviewID: *preview}
	if op == "preview" {
		info, e := os.Stat(*file)
		if e != nil || info.Size() > 1<<20 {
			return errors.New("history_selection_file_unavailable")
		}
		b, e := os.ReadFile(*file)
		if e != nil {
			return errors.New("history_selection_file_unavailable")
		}
		if e = json.Unmarshal(b, &r); e != nil {
			return errors.New("history_invalid_selection")
		}
		r.Operation = "preview"
	}
	if op == "all" {
		r.Operation = "preview"
		r.Mode = "all"
	}
	c, e := config.Load(dir)
	if e != nil {
		return e
	}
	token, e := vault.New(dir).Get("admin-token")
	if e != nil {
		return errors.New("local_credential_missing")
	}
	b, e := (localapi.Client{Address: c.Listen, Token: token}).Post(ctx, "/admin/history", r)
	if e != nil {
		return e
	}
	var out any
	if json.Unmarshal(b, &out) != nil {
		return errors.New("history_invalid_response")
	}
	output(out)
	return nil
}
