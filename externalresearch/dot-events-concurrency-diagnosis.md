# Windows events concurrency diagnosis

Investigated 2026-10-19 on Windows with Go 1.27 and MinGW (`C:/msys64/mingw64/bin/gcc.exe`), `CGO_ENABLED=1`, `-race`. Assigned baseline was fe5fffa; the shared worktree had advanced to 70d2740 (documentation commit) when investigation began. Existing controller edits to `internal/dotevents/store_test.go` (child output capture and wait-all) and `internal/dotbridge/status_capacity_test.go` were preserved. No subagents or live configurations, vaults, or callback endpoints were used.

## Cause and proof

The reproduced failures originate in `store.saveState` when Windows refuses the single `MoveFileEx(REPLACE_EXISTING | WRITE_THROUGH)` attempt with `ERROR_ACCESS_DENIED`. The event-store file lock serializes state operations, but does not prevent temporary Windows handles from denying delete sharing. In particular, another transaction performs `checkAncestors` / `privatePath(dir, true)` before acquiring the lock. That calls `SetNamedSecurityInfo` with inheritable directory ACEs. Windows propagates those to existing children and can overlap the locked writer's replacement. Events previously failed immediately on this temporary denial. Queue already has bounded retries for this exact Windows boundary.

Evidence is retained in `C:/Users/13678/AppData/Local/Temp/opencode/magpie-context-verification/`:

- `dot-events-flake-focused-diagnostic-02.log`: 100 repetitions of both originally failing tests under race. Two cross-process failures and two concurrent-subscription failures reproduce. Every captured unknown stack points to `saveState`'s replacement branch (original store.go:168); subscription fails at its first persistence transaction (manager.go:61), before callback verification / generation finalization. Package fails after 149.619s.
- `dot-events-flake-loaded-diagnostic-03.log`: same target tests alongside queue/bridge concurrency/capacity tests, 100 repetitions. Four captured child failures explicitly report `windows_move_file_ex: Access is denied`, followed by `saveState` stacks. Events finishes in 150.408s. This log does not retain queue/bridge completion results, so it substantiates the captured events failures only.
- `dot-events-flake-acl-isolation-04.log`: an investigation-only test repeatedly replaces a file while a second goroutine reapplies its parent directory ACL. All three repetitions fail on replacement iterations 1, 1, 3 with access denied.
- `dot-events-flake-acl-protected-isolation-05.log`: repeat with protected current-user ACLs on source and target, matching store privacy setup. All three repetitions fail on iterations 2, 0, 3. This isolates ACL propagation as a sufficient trigger, without HTTP, manager generations, or flock contention.
- `dot-events-flake-regression-red-06.log`: committed regression test holds the target with a real Windows handle allowing read/write sharing but denying delete sharing; closes it after 50ms. Existing single-attempt replacement fails with access denied. The missing-source test also checks that the original target remains intact.
- `dot-events-flake-regression-green-07.log`: both regression tests pass with the retry fix, 20 race repetitions, 3.356s.

The first attempted run (`dot-events-flake-focused-diagnostic.log`, UTF-16 PowerShell output) did not build because ucrt64 gcc was absent; it is not test evidence. Subsequent runs use the installed mingw64 compiler and UTF-8 logs.

Temporary instrumentation was installed exclusively by a test init guarded by `DOT_EVENTS_DIAGNOSE=1`; stack locations and Windows stage errors went to test stderr. Public `Error.Error()` remained `dot_events_unknown`. All instrumentation and exploratory isolation tests were removed from the final implementation.

## Alternatives traced

- `withLock` checks directory/path privacy, checks and opens the lock file, verifies hardlink count, closes that checked handle, reapplies lock privacy, and acquires a new gofrs/flock handle. The library uses Windows `LockFileEx` and retries lock violation / pending lock acquisition. It does not replace state. Its handle differs from the prechecked handle, unlike queue's checked-handle locking pattern, but captured failures occur after successful acquisition, at replacement. No locking rewrite is justified by this evidence.
- State, marker, and lock-file `protectFile` checks occur after lock acquisition. State reads, protected-file checks and envelope parsing report corrupt on their failures. Directory and lock-file privacy checks occur before locking; the directory update has child propagation effects. Captured unknown stacks exclude failures at those checks themselves.
- `Subscribe` validates parameters and obtains a generation, writes a provisional record, verifies HTTP outside the lock, then finalizes only the matching generation. Concurrent supersession returns stale. Captured subscription unknown failures occur during the initial `saveState`, not in stale-generation logic or HTTP.
- Remaining unknown paths cover invalid keys/paths/parameters, random-token or encryption failure, marshal/size limits, file creation/write/sync/close, marker creation and link validation. Stack diagnostics distinguish these from the observed replacement branch. On Windows `syncDirectory` is a no-op; replacement instrumentation identifies the failing call directly.
- Both original full-suite failures lasted far less than five seconds. No captured `DATA RACE` report supports a Go memory race, and extending the lock timeout would not address the observed syscall denial.

Microsoft references checked during investigation:

- [SetNamedSecurityInfoW](https://learn.microsoft.com/en-us/windows/win32/api/aclapi/nf-aclapi-setnamedsecurityinfow), Remarks: changing a DACL automatically propagates inheritable ACEs to existing children.
- [CreateFileW](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-createfilew), `dwShareMode`: sharing options last until handle closure; `FILE_SHARE_DELETE` allows delete access, which covers rename operations.

## Fix

`internal/dotevents/files_windows.go` now mirrors queue's one-second bounded retry around `MoveFileEx`, restricted to `ERROR_ACCESS_DENIED` and `ERROR_SHARING_VIOLATION`, with a 10ms interval. It retains the same sealed source, destination, replacement flags, existing lock and privacy checks. Other errors return immediately; persistent denials still fail with the existing redacted outward error. No lock timeout or callback fixture timeout changed.

`internal/dotevents/replace_windows_test.go` exercises a real transient Windows reader and verifies that replacement succeeds, target content changes and source disappears. It also verifies immediate missing-source failure preserves the old target. The controller's child diagnostics remain intact as an existing uncommitted edit; they are excluded from this agent's commit.

## Validation and limits

Commands use the explicit executable `C:/Program Files/Go/bin/go.exe`, `CGO_ENABLED=1`, `CC=C:/msys64/mingw64/bin/gcc.exe`, and prepend `C:/msys64/mingw64/bin` to PATH:

```text
go test -race ./internal/dotevents -run 'TestConcurrentSubscribeAndDeliver|TestCrossProcessLockAndMissingState' -count=100 -timeout=10m
go test -race ./internal/dotevents ./internal/dotqueue ./internal/dotbridge -run 'TestConcurrentSubscribeAndDeliver|TestCrossProcessLockAndMissingState|TestConcurrent|TestCapacity' -count=100 -timeout=10m
go test -race ./internal/dotevents -run '^TestWindowsReplace' -count=20
go test -race ./internal/dotevents -count=30 -timeout=10m
go test -race ./... -count=1 -timeout=10m
```

`dot-events-flake-full-fixed-09.log`: full-suite race run completed. Events passes in 7.580s; queue, bridge and all other packages except cmd/dotcontrol pass. The overall suite is **red** because `TestUninstallWaitsForProcessAfterEngineLockRelease` fails at `install_exit_test.go:89` after 30.11s with `context deadline exceeded`; cmd/dotcontrol finishes failed at 48.670s. This is outside the assigned events scope and was not changed. No DATA RACE report appears. `dot-events-flake-package-fixed-08.log`: all events tests pass for 30 race repetitions in 159.376s, including both previously failing concurrency tests.

`go vet ./internal/dotevents` passes (exit 0; `dot-events-flake-vet-10.log` is empty). `git diff --check` passes.

No external `run_checks` executable/tool is exposed in this subagent session; equivalent Go race commands were run directly.

The ACL experiment proves a sufficient local trigger and manager diagnostics prove the failing syscall; no Windows kernel trace attributes each individual historical denial to a particular ACL propagation handle. Antivirus/scanner handles can produce the same denial, so the fix handles that bounded syscall condition as well. It does not claim to solve arbitrary permanent permission failures or lock-path substitution. The intentionally limited retry can add up to approximately one second per persistently denied replacement. Tests ran on Windows; no Unix execution was performed, and Unix code is unchanged.
