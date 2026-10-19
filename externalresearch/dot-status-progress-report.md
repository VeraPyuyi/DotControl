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
