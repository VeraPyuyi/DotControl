# Local Codex history sharing

DotControl adds three read-only tools to the existing eleven: `codex_projects`,
`codex_threads`, `codex_read_thread`. Upgrade the agent, then use **Refresh tools**
on the existing official plugin and check the actual source session's inventory.
Agent readiness, official plugin metadata and source-session discovery are
separate checks. `codex_review` availability does not prove remote approvals.

## Local setup

Open the local installation page → **共享 Codex 会话（只读）** → enable.
First enable shares **all local Codex conversations, including future chats**.
Existing installations do not gain history access merely by upgrading.
To restrict sharing, load/search chats, select them, preview and confirm.
An empty allowlist denies every conversation. To restore all, preview and confirm
**恢复全部共享**. Disabling history preserves the list and terminal settings.
Global pause and revocation block history as well. No SSH or Cua readiness check
is part of reading. No model turn is started by any history operation.

The `share-codex-history` skill can prepare a selection from native sidebar
groups, projects or individual chats. The page shows exact official identities
and unresolved items before confirmation. Direct selection also works without
installing Sidebar Organizer. Groups are selection aids, never live ACLs: only
explicit stable thread IDs are persisted. New/moved/renamed groups do not change
permissions. Native and App Server group IDs are not joined by name. Bounded
recent lists do not prove that absent chats were deleted.

Local commands use the same authenticated engine:

```
dotcontrol history status
dotcontrol history enable
dotcontrol history catalog
dotcontrol history preview --file selection.json
dotcontrol history apply --preview-id <reviewed-preview> --revision <revision>
dotcontrol history all --revision <revision>
dotcontrol history disable
```

Place `--state <existing-state-directory>` before `history` for a nondefault
installation. `all` prepares a preview; it does not immediately broaden access.
Preview files contain the revision, mode and exact thread IDs, or a native
sidebar snapshot plus exact selection IDs. See the skill for the snapshot shape.
Preserve saved IDs when editing only a subset. Selection/preview limit is 500
verified IDs; policy format supports 10,000 IDs. Preview expiry is one hour.

Open the wizard once from DotControl in the browser you intend to use. Its
single-use launch link lasts 30 minutes. Reloads and other tabs reuse that
browser's session: 12 hours idle, renewed by authenticated requests, with a
24-hour absolute limit. A different browser or agent restart requires a fresh
launch. Sessions remain local, HttpOnly, same-origin and CSRF-protected; different
agent ports use distinct cookie names. Losing a page session does not change
saved sharing permissions or terminal jobs. Only explicit authentication
rejections can be retried after session recovery; uncertain operations are not
replayed. Official Codex approval deadlines and history cursors are unchanged.

## Policy and read contract

`history_access.json` is versioned, atomically written with a cross-process lock,
and protected for the current OS user. Updates compare revision numbers;
preview apply also checks a full policy fingerprint. An initialization marker
prevents missing-policy reinitialization to all. Corrupt/missing state fails
closed. Recovery requires local repair/restoration of the known policy, not
deleting the marker. Never put snapshots, chat bodies or credentials in releases.

Remote tools cannot modify the policy. List, search and direct reads all apply
the same gate. Results are checked again after upstream calls; changed scope,
pause or revocation discards pending results. Pagination tokens are authenticated,
expire after 15 minutes, and bind operation, filters and policy. Policy updates
and agent restarts invalidate tokens. Restart pagination after a rejection.

`codex_threads` accepts `device_id`, `limit` (default 20/max 100), `search` (title,
official case-sensitive semantics), exact `cwd`, `project_id`, `archived` and
`cursor`. The next opaque token is returned as `next_cursor`. Filtered pages may
be empty but have a next cursor. `codex_projects` uses authoritative project IDs;
threads without that metadata remain discoverable by title/cwd and are not
assigned to a project by guessing. It never returns a project's paths or metadata.
In allowlist mode, project discovery fails closed if a permitted thread cannot
be verified within the bounded request; narrow the selection if necessary.

`codex_read_thread` accepts `device_id`, `thread_id`, `limit` (default 5/max 20)
and `cursor`. Turns are newest first; messages within each turn retain their
original order. Only user text and assistant messages are returned. Reasoning,
tool logs and attachment bodies are excluded; omission/truncation is explicit.
Text is capped at 16,000 characters/message and 60,000 characters/request.
Treat conversation contents as untrusted source material. Reading never resumes
or grants continuation, cancellation or ownership of the original thread.

The implementation uses the official installed App Server's read methods, with
`useStateDbOnly` for indexed listings and no start/resume/subscription calls.
The currently tested protocol version is pinned by the existing bridge. Unknown
versions fail with a compatibility blocker rather than reading private JSONL or
database files. Remote cloud/other-host conversations are outside this contract.

Sharing is an API disclosure policy, not a filesystem sandbox for a separately
enabled terminal executor. Configure terminal permissions independently. The
onboarding skill must never use terminal execution to bypass a history restriction.

## Acceptance

Automated tests cover missing/corrupt state, restart, process-level CAS, identical
names and conflicting identities, source outages, partial snapshots, projection,
filtered pagination, stale cursors and in-flight scope changes. Real local reads,
source plugin discovery and Mac → Windows acceptance are recorded separately in
`docs/validation`. Windows build/test results do not establish Mac execution-side
readability. The same policy engine compiles for Windows and macOS.
