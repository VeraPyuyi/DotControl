# Magpie Dot pairing identity fix (I1/I2)

Repository: `dot-automatic-routing` (Magpie whole-branch). Scope: `internal/dotbridge`, `internal/gateway/dot.go`, `internal/cli/dot_cli.go`, `internal/provider/dot.go` (eligibility via `CachedReady` only). GUI files were not edited; GUI worker should wire `dotbridge.PairingIdentity(cfg)` the same way as CLI.

## Problem

- **I1:** Process-global readiness cache ignored pairing changes made in another process or after off/pair/re-pair.
- **I2:** Gateway/CLI journal scopes used caller key + agent (CLI: address + credential ref) without a stable pairing generation, so the same client request ID after re-pair collided with the old queue namespace.

## Design

### Public pairing identity

- `Config` gains `pairing_generation` (random 16-byte hex). **Never** derived from token or token hash.
- `SavePairing` / credential replacement / address or credential ref change / off / clear always mint a **new** generation (`saveConfig(..., replace true)` for pairing).
- `SaveConfig` mints generation only when enabled/address/credential change; toggling `answer_wait_seconds` alone does not rotate generation.
- Legacy configs with credential but empty generation: `LoadConfig` migrates once under `dot-bridge.json.lock` (fail-closed if lock unavailable).

`PairingIdentity(cfg)` = `pair-` + SHA256(JSON `[canonicalLoopbackURL, credentialRef, pairingGeneration]`). Canonical address uses numeric loopback URL from `parseLoopbackAddress` (e.g. `http://127.0.0.1:9`).

### Readiness cache (I1)

- Cache stores `identity` + status + timestamp + generation.
- `Connection.Status` publishes only when `c.pairingIdentity != ""` and `LoadConfig()` still matches that identity and enabled.
- `CachedReady` reloads config and requires `PairingIdentity(cfg) == readyCache.identity` plus existing proof checks.
- `invalidateReady()` on pairing identity or enabled change in `saveConfig`; `SavePairing`/`ClearPairing` use locked paths.
- `NewConnection` without `PairedConnection` does **not** write production readiness.
- `PairedConnection` binds `pairingIdentity` and rejects token load when config identity diverges mid-flight.

### Namespaces (I2)

- Gateway `dotScope` = `pairingIdentity + ":" + hash(keyID, agent)` (request context carries identity from `prepareDot`).
- CLI `dotScope` = `cli:` + `PairingIdentity(cfg)`.
- `serveDot` refuses enqueue when request identity ≠ `conn.PairingIdentity()` ≠ fresh `LoadConfig()`.
- Old journal records remain under old scope; new pairing does not auto-replay or erase them.

## GUI integration contract (for parallel worker)

```go
// After dotbridge.LoadConfig():
scope := "gui:" + dotbridge.PairingIdentity(cfg) // or hash if GUI prefers fixed width
// Replace internal sha256(address+"\x00"+credential) scope helper with PairingIdentity(cfg).
// Empty identity => unmigrated/invalid; do not enqueue.
```

`Connection.PairingIdentity()` returns the identity bound at `PairedConnection()` time.

## Tests added

- `internal/dotbridge/pairing_identity_test.go`: external atomic config write, inflight status, unbound client, off/re-enable, distinct generation same ref.
- `internal/gateway/dot_pairing_identity_test.go`: stable client request ID after `SavePairing` gets new scope/session/request IDs; old journal intact.

## Verification (reviewed head, local)

```text
go test ./internal/dotbridge ./internal/provider ./internal/gateway ./internal/cli -run Dot -count=1
```

All passed. Not run: full `go test ./...`, `-race`, live cfg/vault, GUI.

## Files touched (implementation)

- `internal/dotbridge/config.go`, `client.go`
- `internal/dotbridge/pairing_identity_test.go`, `review_test.go`
- `internal/gateway/dot.go`, `dot_pairing_identity_test.go`, `dot_testutil_test.go`
- `internal/cli/dot_cli.go`, `dot_review_test.go`
- `internal/provider/dot_test.go`

## Follow-up: Important review I1/I2/I3 (2026-10-19)

Read the complete `dot-pairing-identity-review.md` and actual GUI callback implementation at `0ec8af7` before this correction. The original sections above describe ab120e0 and are superseded where they disagree with this section.

- **I1 compatibility:** Before creating a new Attempt/reservation, gateway preparation derives the exact pre-generation scope from authenticated KeyID + agent and the original wire request/session IDs from the stable request header. If that legacy record exists, the request stops with `legacy_request_manual_recovery_required` or `legacy_request_digest_conflict`, original scope and original wire request ID, and `automatic_redispatch:false`. No GET/POST to the current queue is attempted because the unbound legacy scope cannot prove ownership by that queue. Changed content is compared against the original digest using the original session/request IDs. Legacy records are not edited or erased. `magpie dot archives` / `dotbridge.RequestArchive()` expose all retained records with their original scope and wire ID for explicit manual recovery; archive viewing performs no network operations and never reassigns records to a new pairing.
- **I2 off:** Enabled changes no longer rotate PairingGeneration. Off/list/re-enable/resume keep the same scope and wire ID. A separate public random `readiness_revision` changes on enabled/pairing changes and gates status publication/cache consumption, so an off/on cycle in another process cannot revive cached proof even though durable pairing identity remains stable. Actual SavePairing/replacement still always rotates PairingGeneration. Timeout-only edits preserve both revisions. ClearPairing still revokes credentials and rotates identity.
- **I3 binding:** `BoundPairingSnapshot() (Config, *Connection, error)` captures config and connection from one cross-process config lock, including migration and credential lookup. Disabled snapshots return config, nil, errDisabled. Remote scopes MUST be derived from returned config or connection identity, never an earlier independent load. CLI compares initial identity against captured snapshot and derives scope from connection; gateway uses the same helper and checks prepared identity. Bound `Enqueue` checks current pairing AND scope identity before Reserve; bound `Resume` checks before reading the record/querying. A post-POST switch never clears the Attempt barrier.

### GUI worker/controller exact integration contract

Keep initial LoadConfig only for local disabled/off/list behavior. Replace later PairedConnection call with:

```go
boundCfg, c, err := dotbridge.BoundPairingSnapshot()
if err != nil || dotbridge.PairingIdentity(boundCfg) != dotbridge.PairingIdentity(cfg) {
    // Return pairing-changed/unverified before any journal or remote operation.
    return
}
scope = "gui:" + c.PairingIdentity()
// All Status / Enqueue / Resume / Cancel calls for this operation use c.
```

No GUI file was edited by this worker. `RequestArchive()` returns `[]ArchivedRequest` with `Scope string json:"scope"` plus embedded Snapshot (original request_id/digest/state/session/job/mode/barrier). It is local inspection only; do not feed archive scopes to the current paired connection. Disabled current-scope list remains valid because off preserves PairingGeneration. The exported helper and bound-client scope checks support the GUI worker's concurrent correction.

### Regression evidence

Before implementation, real behavioral FAILs: `TestDotLegacyStableHeaderNeverReposts/{same,conflict}` each observed 1 new POST; `TestDotReviewOffPreservesPendingNamespace` reported stranded namespace; `TestDotReviewOffResumeUsesOriginalWireID` made zero GETs and failed request-not-found; `TestDotReviewSwitchBeforeReserveLeavesNoRecord` found a new acceptance_unknown record; `TestDotReviewConnectionRejectsForeignScope` accepted foreign scope. After implementation, focused dotbridge/provider/gateway/CLI Dot suites pass. Additional tests check archive listing, deterministic independent atomic config switch during snapshot construction, and post-POST barrier preservation. Final implementation commit: `a3a339e` in Magpie. Final CGO/MinGW `go test -race ./internal/dotbridge ./internal/provider ./internal/gateway ./internal/cli -run Dot -count=1` passed (dotbridge 6.843s, provider 3.888s, gateway 32.235s, CLI 7.013s). Full `go test ./internal/dotbridge -count=1` passed. `go vet ./internal/dotbridge ./internal/gateway ./internal/cli ./internal/provider`, gofmt, and `git diff --check` passed. First race invocation timed out at 60s; the subsequent complete runs passed with a larger command budget. Full repository suite, live cfg/vault/accounts, and this worker's GUI tests were not run. GUI remains owned by the parallel GUI worker; this commit stages only the eight owned config/client/journal/gateway/CLI implementation and regression files.
