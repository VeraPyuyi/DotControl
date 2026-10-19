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
