# Dot queue List slice report

## Scope

Implemented `Store.List(clientID string) ([]Job, error)` in `internal/dotqueue/list.go` with behavioral tests in `internal/dotqueue/list_test.go` and documentation in `docs/DOT_TASK_QUEUE.md`.

## Behavior

- Validates `clientID` with `validateID` before locking.
- Uses `withLock(false, false)` and rejects when `!st.Enabled` (`dot_queue_disabled`).
- Iterates persisted `order` in reverse so results are newest-first by creation order.
- Returns only jobs whose `client_id` matches the caller; foreign clients never appear in another owner's list.
- Maps each record through `copyPublicJob`, which builds a public `Job` and deep-copies `Result` (including artifacts) so returned values do not alias internal `jobRecord` fields.
- Does not mutate `jobRecord` or persistence state during listing.
- Retention remains bounded by the existing 100-job store limit; List does not evict or create jobs.
- On error returns `(nil, err)`; on success returns a non-nil empty slice when the owner has zero jobs.

## Tests (RED/GREEN)

| Test | Intent |
|------|--------|
| `TestListRejectsInvalidClientID` | Empty client ID fails before read |
| `TestListDisabledFailsClosed` | Disabled gate matches Get/ByRequest |
| `TestListOwnerForeignAndZero` | Empty owner list, foreign isolation, newest-first order |
| `TestListIncludesTerminalStatesAndStableReopen` | Cancelled/completed included; order stable after reopen |
| `TestListOmitsSensitiveFieldsAndDoesNotAlias` | JSON omits prompt/token/payload fields; copies do not alias store |

## Verification commands

Working directory: `D:\Applications\Opencode\data\share\worktree\bd2540\dot-task-event-bridge`

```powershell
& "C:\Program Files\Go\bin\go.exe" test ./internal/dotqueue -count=1 -run TestList -v
& "C:\Program Files\Go\bin\go.exe" test -race ./internal/dotqueue -count=1 -run TestList
```

Results: all `TestList*` cases passed on 2026-10-19. `-race` was attempted with `CGO_ENABLED=1` but the environment has no C compiler (`gcc` not found); rerun `-race` where CGO and a compiler are available.

## Follow-up

HTTP/controller `list` GET adapter wiring is intentionally out of scope for this slice.
