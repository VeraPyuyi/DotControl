# Dot queue capacity visibility slice report

## Scope

Owned changes only:

- `internal/dotqueue/store.go` — new submissions at the global retained limit return `queue_full`.
- `internal/dotqueue/capacity.go` — `CapacityStatus` and `Store.Capacity()`.
- `internal/dotqueue/capacity_test.go` — behavioral and cross-process coverage.
- `docs/DOT_TASK_QUEUE.md` — operator semantics and error code documentation.

Out of scope (controller/API): HTTP 429 mapping, DotControl UI wiring.

## Behavior

- Global retained limit remains `maxRetainedJobs` (100), counting terminal jobs; no eviction or request-index tombstone erasure.
- `Submit` idempotency is unchanged: same client/request with identical digest succeeds even when full; changed digest still returns `conflict`.
- New client/request pairs when `len(jobs) >= 100` return `queue_full` (not `conflict`).
- `Capacity()` uses `withLock(false, false)`, rejects when disabled (`dot_queue_disabled`), and returns `{Limit, Retained, Remaining}` only.
- `ConfigStatus()` remains the separate enabled gate without capacity fields.

## Tests (RED → GREEN)

| Test | Intent |
|------|--------|
| `TestCapacityStatusWhileFilling` | Limit/retained/remaining while empty and full |
| `TestCapacityQueueFullAcrossClients` | 101st `queue_full`, no extra job; idempotent + changed-conflict at full; reopen stable counts |
| `TestCapacityDisabledDenied` | Disabled gate on `Capacity()` |
| `TestCapacityEncryptedPersistence` | Counts survive reopen; no plaintext request ids on disk |
| `TestCapacityCrossProcessSubmitAtFull` | Child submit at full returns `queue_full`; parent count stays 100 |
| `TestCapacityConcurrentIdempotentAtFull` | Two-handle race for same request at capacity returns original job |

## Verification commands

Working directory: `D:\Applications\Opencode\data\share\worktree\bd2540\dot-task-event-bridge`

```powershell
& "C:\Program Files\Go\bin\go.exe" test ./internal/dotqueue -count=1 -run TestCapacity -v
& "C:\Program Files\Go\bin\go.exe" test ./internal/dotqueue -count=1 -v
```

Optional race (requires CGO + C compiler):

```powershell
$env:CGO_ENABLED=1
& "C:\Program Files\Go\bin\go.exe" test -race ./internal/dotqueue -count=1 -run TestCapacityConcurrentIdempotentAtFull
```

## Follow-up

- `internal/dotqueue/store_test.go` `TestMaxRetainedJobs` still expects `conflict`; update to `queue_full` when that file is in scope.
- API worker should map `queue_full` → HTTP 429 in the controller slice.
- Archive/migration slice required before operators can accept job 101+ without manual store reset.
