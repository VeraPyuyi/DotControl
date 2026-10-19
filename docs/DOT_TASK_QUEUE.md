# Dot task queue (`internal/dotqueue`)

Task 1 delivers a durable, encrypted filesystem queue for DotControl routing work. Network exposure, MCP tools, vault wiring, and runtime gates are intentionally out of scope here.

## API surface

- `Open(dir, key)` — absolute state directory and 32-byte AES-256-GCM key supplied by caller (never persisted).
- `Store.Initialize(enabled)` — explicit first-time setup; writes `dot_queue.initialized` and refuses silent re-init if state is missing after the marker exists.
- `Store.SetEnabled(bool)` — toggles whether sensitive mutations are accepted.
- `Submit`, `Get`, `ByRequest`, `Claim`, `Progress`, `Complete`, `Cancel`, `Recover`, `Pending` — semantics match `docs/superpowers/specs/2026-10-19-dot-routing-design.md` and the task brief.

Exported structs use JSON snake_case field names. Public `Job` values omit claim tokens and other worker secrets.

## Storage and concurrency

- Private directory (`0700`) and files (`0600`) with Windows current-user DACL, matching `internal/historyaccess` patterns.
- Cross-process `flock` on `dot_queue.lock` (5s acquisition budget, 10ms retry interval).
- All queue state is held in a single encrypted blob `queue.state.dq` (`D` + version byte + nonce + AES-GCM ciphertext). Prompts, results, and claim tokens are not stored in plaintext on disk.
- Updates are read → validate → mutate → temp file → sync → atomic replace under the lock.

## Lifecycle rules

- **Idempotency:** same `client_id` + `request_id` + submission digest returns the same job; digest mismatch is `dot_queue_conflict`.
- **Capacity:** at most 100 retained jobs; overflow returns conflict (no eviction).
- **Claim:** `queued` → `claimed` with a 256-bit token; duplicate claim by the same worker returns the same token; another worker gets `dot_queue_claim_conflict`.
- **Cancel:** before claim → `cancelled`; after claim → `cancel_requested` (progress does not clear cancel intent).
- **Complete:** terminal and idempotent on identical result digest; mismatched terminal payloads conflict.
- **Recover:** explicit only (not on `Open`); moves in-flight worker states to `outcome_unknown` once per job after restart uncertainty.
- **Disabled:** sensitive operations return `dot_queue_disabled`; read APIs remain available.

## Error codes

Stable `dotqueue.Error.Code` values include: `disabled`, `conflict`, `notfound`, `corrupt`, `claim_conflict`, `unknown`.

## Tests

Package tests cover encryption round-trip, reopen persistence, idempotency, claim/complete conflicts, cancel/recover, disabled gating, corrupt/missing state, bounds, concurrency across two `Store` handles, and capacity limits. Run:

```text
go test ./internal/dotqueue/ -count=1
python run_checks.py <repo> dotqueue-race go test ./internal/dotqueue/ -race -count=1
```
