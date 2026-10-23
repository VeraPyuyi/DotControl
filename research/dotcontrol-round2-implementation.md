# DotControl round 2 implementation

## 2026-10-23: reject conflicting launch definition executables

Base: `cec0e0848f375277a420b44e10e5cc4630de545c`, branch `codex/macos-lifecycle-rebuild`.

The requested research file was absent in this checkout; this entry creates it.

`matchesExecutable` previously preferred ManagedData and ignored a remaining login plist. A same-state login definition pointing at an old executable could therefore pass ownership validation and its positive loaded PID could make StartManaged return success for the wrong installation.

The shared executable check now validates every present background/login plist and, when present, the loaded program against the requested executable. SnapshotStartup's existing source, program, and complete argument checks remain in effect before StartManaged receives a snapshot. launchStartAction returns `conflicting_definition_paths` before inspecting the PID whenever the executable check fails. State ownership failures retain their existing reason. The shared check also prevents a conflicting snapshot from being classified as managed in startup status.

### RED / GREEN evidence

Added `TestLaunchPolicyChecksAllDefinitionPaths` with eight pure snapshot fixtures:

- different-paths-loaded-login
- different-paths-loaded-background
- same-path-loaded-login
- same-path-loaded-background
- stale-login-unloaded
- stale-background-unloaded
- stale-loaded-program
- same-path-unloaded

Before the implementation change, `go test -count=1 ./internal/desktop -run '^TestLaunchPolicyChecksAllDefinitionPaths$'` exited 1: five failing and three passing subtests. The old loaded login and stale loaded program returned success, and the stale unloaded login returned bootstrap. After the fix, `go test -count=1 ./internal/desktop -run '^TestLaunch(Policy|Registration)'` exited 0. Existing path-mismatch coverage now expects the explicit conflict reason.

### Full verification

Executed on Windows with Go and MinGW added only to each child shell's PATH; CGO_ENABLED=1 applied only to the race child shell. No persistent user environment changes.

| Command | Exit | Passed test events (including subtests) | Passed top-level tests | Passed packages | Skipped test events |
| --- | --- | --- | --- | --- | --- |
| `go test -json -count=1 ./...` | 0 | 1320 | 635 | 22 | 10 |
| `CGO_ENABLED=1 go test -race -json -count=1 ./...` | 0 | 1320 | 635 | 22 | 10 |
| `go vet ./...` | 0 | n/a | n/a | n/a | n/a |
| `go build ./...` | 0 | n/a | n/a | n/a | n/a |

Both test runs had zero failures and one package with no tests. Counts use actual JSON event lines; PowerShell's native stdout decoding damaged one Chinese subtest name, so whole-line JSON parsing would undercount by one. Event-line counting includes that passing event. Logs: `C:\Users\13678\AppData\Local\Temp\opencode\dotcontrol-paths-test.json` and `dotcontrol-paths-race.json`.

Both runs skipped the same ten test events:

- TestOfficialHistoryReadOnlyAcceptance
- TestCrashDispatchSubprocess
- TestLiveFixtureObservation
- TestLocalCodexDiscovery
- TestStoreRefusesSymlinkState
- TestCleanupRejectsUnsafeOwnedPaths/symlink
- TestRejectsLinks/symlink
- TestNativeWindowsStartupLifecycle
- TestOwnershipDoesNotCaseFoldDistinctPaths
- TestOwnedFileRejectsRecordedSymlinkAlias

No system service was installed or started and no login item was changed. Native macOS launchd execution was not tested on this Windows host; the regression fixtures exercise the platform-neutral policy used by the Darwin implementation. Missing loaded-program evidence in synthetic policy snapshots remains accepted for compatibility with existing tests; production SnapshotStartup already rejects missing or mismatched loaded programs before invoking this policy.
