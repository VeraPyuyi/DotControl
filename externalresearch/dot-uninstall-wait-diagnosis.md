# Windows uninstall process-wait diagnosis

Investigated 2026-10-19 in `D:/Applications/Opencode/data/share/worktree/bd2540/dot-task-event-bridge`, starting at `9705d8f`. The shared worktree advanced to `3549c5f` while checks ran; the controller committed its existing capacity synchronization and events child diagnostics as `a2882f6`. This worker did not edit events or bridge files, live configuration, installed services, startup registrations, or credentials. All fixture credentials used `keyring.MockInit`. No subagents were used, and no unrelated machine process was signaled.

## Windows references

- [PROCESSENTRY32](https://learn.microsoft.com/en-us/windows/win32/api/tlhelp32/ns-tlhelp32-processentry32): parent field is the identifier of the creator process.
- [Why is CreateToolhelp32Snapshot returning incorrect parent process IDs?](https://devblogs.microsoft.com/oldnewthing/20150403-00/?p=44313): explains stale parent IDs after reuse and comparing creation times to reject an impossible parent relationship.
- [When does a process ID become available for reuse?](https://devblogs.microsoft.com/oldnewthing/20110107-00/?p=11803): explains process-object and handle lifetime, used when examining the fixture's `cmd.Wait` behavior.

## Root cause and direct reproduction

`dot-events-flake-full-fixed-09.log` originally failed `TestUninstallWaitsForProcessAfterEngineLockRelease` at `install_exit_test.go:89` after 30.11 seconds with `context deadline exceeded`. The fixture should exit 500ms after receiving its exit file, and has an independent ten-second watchdog. Extending the shutdown timeout would not explain or fix this failure.

The reproduced bug is in process-tree descendant discovery, not the fixture's exit delay. Windows `PROCESSENTRY32.th32ParentProcessID` retains the original creator PID after that creator exits. A later process can reuse that PID. The traversal previously treated any parent-PID equality as proof of descent, recursively, and retained the resulting PIDs indefinitely.

`dot-uninstall-minimal-diagnostic-06.log` reproduces the same 30.11-second failure twice in 100 race repetitions. Its first failure records:

```text
stop begin pid=17540 at=2026-10-19T16:00:05.5518654+08:00
observe begin pid=17540
tree add pid=19152 parent=17540 name=conhost.exe
tree add pid=31488 parent=19152 name=OneDrive.Sync.Service.exe
observed ids=[17540 19152 31488]
shutdown unlocking pid=17540 at=2026-10-19T16:00:05.5752782+08:00
wait end ids=[17540 19152 31488] err=context deadline exceeded
child pid=17540 wait=<nil>
child received exit at=2026-10-19T16:00:05.5835182+08:00
child exiting at=2026-10-19T16:00:06.0841138+08:00
```

A read-only `Get-Process -Id 31488 | Select-Object Id,ProcessName,StartTime` showed `OneDrive.Sync.Service` started on **2026-10-18 at 16:28:08**, almost a day before the fixture's console host. It cannot be that newly created host's child. The second failure in the same log again adopts PID 31488 through a new host that reused PID 19152. The unrelated process was never stopped. The test child exited with success and was waited for in both failures.

Investigation overlays captured child output, wait completion, adopted PIDs and, in a separate diagnostic variant, Windows creation times. The reproducing minimal variant kept the original nil stdout/stderr streams and wrote its child diagnostics inside the fixture's temporary directory. Repository production sources were not instrumented. Every fixture cleanup requested exit and waited; its fallback termination was restricted to the exact child created by that test.

## Baseline proof and deterministic regression

The following sources are byte-for-byte equivalent after newline normalization at `4208e05` and `9705d8f`: `cmd/dotcontrol/install_exit_test.go`, `cmd/dotcontrol/install.go`, `internal/desktop/references.go`, and `internal/desktop/references_windows.go`. A `git archive` copy of `4208e05` was extracted outside the worktree for comparisons.

The deterministic regression models the observed process table: root 17540 created at time 200, console host 19152 at 300, and unrelated process 31488 at 100 with its stale parent PID still 19152. It also checks the next snapshot after the owned processes disappear, so merely filtering the initial snapshot cannot satisfy it.

- `dot-uninstall-regression-red-08.log`: the current traversal fails, adopting `[17540 19152 31488]`.
- `dot-uninstall-base-regression-red-09.log`: the same fixture fails identically on `4208e05`. The overlay only extracts the existing traversal into a callable step and adds an unused creation-time field; it changes no traversal decision.
- `dot-uninstall-base-final-red-18.log`: the final regression fixtures also fail on the base's unchanged traversal and snapshot reader, including the native snapshot age check. The overlay adds only the extraction, unused metadata fields and fixtures.
- `dot-uninstall-base-final-green-17.log`: overlaying the final helper and snapshot changes onto that same base passes all final process-tree and native snapshot regressions for 30 race repetitions.

This proves a preexisting process-discovery bug. It is not a basis for waiving the original full-suite failure; the bug is fixed here. The historical log lacked process diagnostics, so the exact PID adopted in that historical invocation is unknown. The reproductions match its test, deadline, and error, and directly identify the faulty operation.

## Fix and preserved behavior

Windows process snapshots now obtain creation `FILETIME` using `GetProcessTimes` on the existing `PROCESS_QUERY_LIMITED_INFORMATION` handle. Live observation retains creation evidence for each adopted process and rejects a proposed child only when both times are known and the child predates its purported parent. Evidence survives later snapshots after parent exit, preventing delayed adoption of the same stale relationship.

Unknown creation times continue to block conservatively. Genuine descendants, equal timestamps, orphaned descendants, and occupied previously recorded PIDs still block exit. `WaitProcessIDs` recovery records contain only PIDs; creation filtering is disabled for that recovery path, preserving its original conservative interpretation of uncertain ancestry. A separate regression caught and prevented weakening recovery during implementation.

The same traversal still watches descendants accepted while shutdown drains. There is no timeout increase, forced production termination, process-name exemption, PID removal, or change to shutdown endpoints. Unix snapshot creation values stay zero, leaving Unix traversal behavior unchanged. This narrowly changes the proven false-descendant case in the shared stop helper used by uninstall and upgrade; public shutdown behavior otherwise stays as before.

`install_exit_test.go` now checks fixture file writes, captures stdout/stderr, and always waits for its child during cleanup before inspecting output or allowing temporary-directory cleanup. Failed runs report the child PID, wait result and output. The existing 500ms delay and production 30-second shutdown deadline remain unchanged.

## Verification

All logs and runner scripts are under `C:/Users/13678/AppData/Local/Temp/opencode/magpie-context-verification/`. Commands use the existing `run_checks.py --race` sandbox, `C:/Program Files/Go/bin/go.exe` (Go 1.27), `CGO_ENABLED=1`, and `CC=C:/msys64/mingw64/bin/gcc.exe`, with that compiler directory on PATH. HOME, APPDATA, USERPROFILE and temporary directories are isolated while the existing Go caches are retained.

Final checks:

```text
go test -race ./internal/desktop ./cmd/dotcontrol -run '^TestProcessTree|^TestWindowsProcessSnapshot|^TestUninstallWaitsForProcessAfterEngineLockRelease$' -count=30 -timeout=5m
go test -race ./... -count=1 -timeout=10m
go vet ./internal/desktop ./cmd/dotcontrol
```

- `dot-uninstall-final-focused-12.log`: passes all focused checks for 30 repetitions (desktop 2.548s, CLI 58.362s; runner 62.31s).
- `dot-uninstall-full-final-13.log`: **full repository race suite passes**, exit 0, runner 65.46s. CLI 13.391s, desktop 4.282s, bridge 50.452s, events 6.597s, queue 47.762s.
- `dot-uninstall-vet-14.log`: targeted vet passes, exit 0.
- `dot-uninstall-final-loaded-15.log`: **all packages pass** for 100 race repetitions with the original fixture's nil streams and final production source. Concurrent packages are CLI (178.331s), queue (58.279s), and events (151.644s); runner 183.24s. Command: `go test -race -overlay=.../uninstall-diagnosis/final-load-overlay.json ./cmd/dotcontrol ./internal/dotqueue ./internal/dotevents -run '^TestUninstallWaitsForProcessAfterEngineLockRelease$|^TestConcurrentSubmitAndClaim$|^TestConcurrentSubscribeAndDeliver$|^TestCrossProcessLockAndMissingState$' -count=100 -timeout=10m`.
- `gofmt` and `git diff --check` pass for the final changes.

Other completed investigation runs and limitations:

- Original focused fixture: 20 race repetitions passed (`dot-uninstall-original-focus-01.log`). Captured-output diagnostics: 50 fixture repetitions passed alongside package load (`dot-uninstall-diagnostic-load-02.log`); 50 baseline fixture repetitions passed (`dot-uninstall-base-diagnostic-03.log`); four concurrent test processes each passed 50 repetitions (`dot-uninstall-stress-05.log` and per-worker logs). These passes did not establish absence of the bug. The minimal variant subsequently reproduced it twice.
- `dot-uninstall-handle-probe-07.log` retains an additional handle to the owned child; the wait passes. That experiment did not support the theory that retaining an exited process handle by itself blocks the process-table wait.
- `dot-uninstall-diagnostic-load-02.log` is **not an overall green run**: the queue package reached the total 15-minute budget during repeated capacity fixtures. Its stack is executing JSON parsing in `fillStoreJobs` for `TestCapacityQueueFullAcrossClients`; no queue behavior conclusion is drawn. CLI, bridge and events completed successfully.
- `dot-uninstall-full-diagnostic-04.log` is **red**: all three uninstall repetitions pass, but `TestWriteAfterPauseStaysConservative` reports an actual `DATA RACE`. The write is `internal/dotbridge/http_budget_test.go:200`; the concurrent read is `Service.gate` at `service.go:140`, via the worker authorization path. Bridge files are outside this worker's ownership and were left untouched. The final single-count full suite is green, but that does not erase or waive this intermittent race finding; the controller has a concrete trace to address.
- `dot-uninstall-recovery-red-11.log` intentionally fails the additional recovery constraint before restricting creation filtering to live observation. The final focused run includes the passing recovery regression.
- `dot-uninstall-base-final-red-16.log` is a build failure from a stale diagnostic overlay lacking the new unused map field; it is not behavioral red evidence. The earlier base regression red is valid.

No race checks were disabled or waived. No non-Windows tests were executed; the shared traversal's Unix behavior is preserved by unavailable creation metadata.
