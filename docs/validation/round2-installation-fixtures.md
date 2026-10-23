# Round 2 installation audit

Baseline `c90155c52e7069540619cd4766bccbcd6a3dbcce` already implements T2. No installer behavior is replaced from the older PR #2 branch.

| Requirement | Existing fixture evidence |
|---|---|
| Suspend before stopping; registered startup | `TestUpgradeSuspendsBeforeExitAndStartsRegisteredRuntime` |
| Failure rollback startup/config consistency | `TestFailedHealthSuspendsCandidateBeforeRollbackExit`, `TestUnmanagedActivationFailureRestoresExactConfigStartupAndLegacy` |
| Interrupted activation recovery | `TestMissingExecutableRepairRecoversInterruptedActivation`, `TestInterruptedUnmanagedActivationRecoversFromDurableSnapshot` |
| Missing old program; repair retry | `TestManagedMissingExecutableRepairAndFailedHealthRetry`, `TestManagedMissingExecutableStillRequiresExitEvidence` |
| Preserve device config, pause, revocation, sharing scope | `TestManagedUninstallAndReinstallPreservesState` |
| Cleanup only recorded owned payloads; preserve unknown/changed files | `internal/install/cleanup_test.go` |
| Retain state binding after reinstall/interruption | `TestRetainedReceiptSurvivesActivationBeforeCommitRecovery`, `TestManagedStateBindingSurvivesCleanupAndRejectsRebinding` |

The external round 2 implementation report records actual command results, test counts and platform skips. These are filesystem/command fixtures; they do not establish real macOS installer or launchd acceptance. When the old executable is absent, recovery reports that it cannot restart that old executable and allows repair; it does not claim successful runtime restoration.
