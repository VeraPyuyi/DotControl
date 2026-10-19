# Dot status/progress/capacity crosswire report

Date: 2026-10-19. Scope: whole-branch review items I3, I4, and I5 only.

## Summary

DotControl now exposes enabled-gated aggregate queue capacity through admin and paired status, carries durable progress summaries through the version-1 job projection and local GET/request/list surfaces, and fixes the operator CLI response budget so valid escaped results are not silently truncated.

Magpie adds matching DTO fields for `Job.summary` and `Status.capacity` so controller/GUI consumers can render additive status without changing identity-worker ownership.

## DotControl changes

- `internal/dotbridge/adapters.go`
  - Added `QueueAPI.Capacity()` and queue adapter delegation.
- `internal/dotbridge/types.go`
  - Added `CapacityDTO` and `StatusDTO.capacity`.
  - Added `JobDTO.summary`.
- `internal/dotbridge/service.go`
  - Added `bridgeCapacity()` with enabled gate before and after queue I/O.
  - `AdminStatus()` and `statusDTO()` now include aggregate capacity when enabled.
  - `toJobDTO()` now projects `Job.Summary`.
- `internal/dotbridge/api.go`
  - Exported `MaxWireResponseBytes` for shared CLI/client budget alignment.
- `cmd/dotcontrol/dot_bridge.go`
  - Added `readBridgeResponseBody()` with max+1 overflow detection and read-error propagation.
  - Removed the old 1 MiB silent truncation path.
- Tests
  - `internal/dotbridge/status_capacity_test.go`
  - `internal/dotbridge/job_summary_test.go`
  - `cmd/dotcontrol/dot_bridge_response_test.go`

## Magpie changes

- `internal/dotbridge/types.go`
  - Added `Capacity` and `Status.capacity`.
  - Added `Job.summary`.
- `internal/dotbridge/types_test.go`
  - Added JSON field coverage for the new DTO fields.

## Behavior notes

- Capacity is aggregate-only: `limit`, `retained`, `remaining`.
- Capacity is omitted when the bridge is disabled, preventing disabled-state count leakage.
- Probe jobs and terminal jobs count toward retained capacity.
- `queue_full` remains the stable human-facing rejection reason at the storage boundary.
- Job summaries are bounded by the existing queue store limit (2048 UTF-8 bytes) and are retained on GET, request lookup, and list metadata.
- Server raw result budget (256 KiB) and encoded wire budget (2 MiB) were not changed.

## Verification

DotControl:

```powershell
& "C:\Program Files\Go\bin\go.exe" test ./internal/dotbridge/ -run "TestJobSummary|TestAdminAndPaired|TestCapacityFull" -count=1 -v
& "C:\Program Files\Go\bin\go.exe" test ./cmd/dotcontrol/ -run "TestBridgeClient|TestReadBridge" -count=1 -v
& "C:\Program Files\Go\bin\go.exe" test ./internal/dotbridge/ -count=1
```

Magpie:

```powershell
& "C:\Program Files\Go\bin\go.exe" test ./internal/dotbridge/ -run TestJobAndStatusSummaryCapacityFields -count=1 -v
```

Observed: all scoped tests PASS.

## Out of scope

- I1 readiness cache identity binding
- I2 gateway journal namespace pairing identity
- GUI rendering/controller wiring beyond additive DTO fields
- Archive/eviction semantics for retained jobs

## Capacity publication review fix — 2026-10-19

Addressed Important I1 and the scoped test observations in `dot-status-progress-review.md`. The earlier implementation's enabled checks did not protect the final publication: a revoked client could receive counts and admin enabled/counts could describe different lifecycle snapshots. This was a production defect, not waived as a minor test issue.

`bridgeCapacity` now returns a candidate plus the service enable epoch, without holding the service mutex during queue I/O. Enable/Disable advance that epoch. AdminStatus builds enabled, client totals and capacity inclusion together under its final lock. Paired status attaches counts only under its final lock when enabled, unpaused, access not revoked, current client still authorized with the same credential identity, and the epoch matches. A disable/re-enable invalidates old counts even if final enabled is true. Client revocation suppresses paired counts and reports paired:false; authenticated admin aggregate visibility survives individual client revocation. Capacity storage errors still propagate for an authorized admin; intervening access changes suppress stale data/error publication.

RED: `go test ./internal/dotbridge -run TestCapacityPublicationAccessChanges -count=1` failed on paired revoke/pause/access revoke/disable-enable and admin disable/disable-enable/pause/access revoke. GREEN: the same matrix passes after the production change. Deterministic hooks run after real queue I/O and before publication; additional channel-blocked tests execute Disable/RevokeClient in another goroutine before releasing the captured result. Disabled-before tests assert zero Capacity calls. No queue I/O is performed while holding the service mutex.

CLI fixture now uses json.Marshal of a 256 KiB raw '<' result (encoded body exceeds 1 MiB), serves it over actual HTTP, captures stdout in a regular file and decodes the complete result. Its HTTP overflow variant asserts bridge_response_too_large and zero stdout. Existing exact 2 MiB/+1 helper boundaries and short HTTP body tests remain.

Verification on the final fix:

- `go test ./internal/dotbridge ./cmd/dotcontrol -run 'TestCapacity|TestAdminAndPaired|TestBridgeClientResponse|TestReadBridgeResponse' -count=1` PASS.
- `go test -race ./internal/dotbridge ./cmd/dotcontrol -run 'TestCapacity|TestAdminAndPaired|TestBridgeClientResponse|TestReadBridgeResponse' -count=1` PASS (CGO_ENABLED=1, CC=C:\msys64\mingw64\bin\gcc.exe; final run 7.241s / 1.887s).
- `go test ./internal/dotbridge -count=1` PASS (6.620s).
- `git diff --check` PASS.

No GUI, Magpie identity/client, queue storage/proof, ACL, raw-content or wire-budget production semantics changed. These checks establish the scoped regression fix, not live Dot or whole-branch completion.
