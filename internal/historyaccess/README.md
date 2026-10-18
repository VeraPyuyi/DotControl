# Local history access

`NewStore(stateDir)` uses `history_access.json`. Only an explicit user enable
may call `InitializeAll`; reads and updates never synthesize defaults. Version
1 policies contain `version`, `mode`, `revision`, and `allowed_thread_ids`.
Unknown, missing, duplicate, wrongly cased, or null fields fail closed. The file
is bounded to 1 MiB and 10,000 input IDs. IDs must be lowercase UUID strings.

`all` has an empty ID array. `allowlist` grants only its sorted, deduplicated
IDs; an empty allowlist denies every thread. `Apply` compares the expected
revision under a cross-process lock and advances it once. `Fingerprint` hashes
the complete normalized policy including revision, so manually editing IDs at
the same revision invalidates a prepared review. Invalid policies have an empty
fingerprint and grant no access through `Allows`.

A private `history_access.initialized` marker prevents a missing policy after
initialization from silently reverting to `all`, including after restart.
The marker is flushed before the first policy write, so an interrupted first
enable can require local recovery rather than silently granting broad access.
Do not remove that marker as an automatic repair. Parent directories of the
supplied absolute state directory must be trusted.

Readers and writers share `history_access.lock`, waiting at most five seconds.
Writes use a private temporary file, file flush, and same-directory atomic
replacement. Unix additionally flushes the directory; Windows uses protected
current-user-only ACLs and replacement with write-through. Missing policy is
`history_access_missing`; other fixed errors are `history_invalid_policy`,
`history_revision_conflict`, `history_revision_exhausted`,
`history_invalid_state_dir`, `history_storage_unavailable`, and `history_locked`.
No error includes filesystem paths or supplied JSON.

`ResolveSidebar` accepts native metadata in this shape:

```json
{
  "projects": {"projects": []},
  "sidebar": {
    "pinnedThreads": [],
    "threads": [],
    "sections": [],
    "unavailableHosts": [],
    "unavailableSources": []
  }
}
```

All shown arrays are required and cannot be null. Additional native fields
inside the two objects and rows are retained in the snapshot hash. Only those
two top-level fields are accepted. Input is bounded to 8 MiB, with duplicate
JSON keys and nesting beyond 64 levels rejected. Invalid snapshot/selection
errors are `history_invalid_snapshot` and `history_invalid_selection`.

Selections refer to exact `sectionId`, `projectId`, or stable thread `id` values.
Section entries use native item keys such as `codex:thread:local:<UUID>` and
`codex:project:<projectId>`. Threads must explicitly identify `kind: codex` and
`hostId: local`; projects must explicitly identify `projectKind: local` and
`hostId: local`. Names and paths never establish identity. A consistent row in
recent and pinned lists is allowed. Conflicting identities or exclusive
custom/pinned memberships are skipped. Default sidebar section membership is
an overlay and does not conflict with custom membership.

The returned IDs and issues are deterministic. The native lists cover only a
subset of history: `complete` is always false and project expansion reports
`incomplete_project_membership`. Stable section references absent from the
listed rows report `missing_thread`; they are never guessed from aliases.
Callers may separately use an official exact-ID lookup to resolve such entries.

The resolver has no tool, filesystem, history-body, or sidebar-mutation access.
Its candidate JSON is untrusted input to a separate official identity check and
local human review; only `Store.Apply` after that review changes consent.
