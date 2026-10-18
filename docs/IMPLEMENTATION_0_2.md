# DotControl 0.2 implementation and release gates

This is the early alpha.1 implementation record. See the [0.2 series review](REVIEW_0_2.md)
for later failures, seven source fixes after alpha.6, regression coverage and remaining limits.

Approved scope: Windows 11 x64 and macOS 14+ arm64/amd64 user installers,
one shared install engine and local onboarding UI, a Codex onboarding plugin,
independent official tunnels, and capability-gated target Codex delegation.
Target prerelease: `VeraPyuyi/DotControl`, `v0.2.0-alpha.1`.

## Work tracking

- [x] Preserve 0.1 baseline and run existing Go tests.
- [x] Record official App Server native Computer Use capability probe.
- [x] Transactional install, recovery, retained identity and uninstall.
- [x] Local onboarding, OS credential entry and independent connection checks.
- [x] User-session startup, process recovery and persistent pause/revoke.
- [x] Windows installer, Mac prebuilt app/DMG and tray/menu bar.
- [x] Codex onboarding plugin and webpage assistance.
- [x] Capability-gated Codex MCP contract.
- [x] Security/regression reviews, packaging and downloaded-byte verification.
- [ ] Real Windows and Mac installation and cross-device acceptance.
- [x] GitHub prerelease and explicit support matrix.

Implementation checks mean the code and focused automated checks are complete, not that a new installation has passed desktop acceptance. The App Server probe found a partial native route; native execution remains disabled.

## Evidence rules

An old cross-device calculator result verifies the old installed bytes only.
CI compilation is not desktop acceptance. A running process is not a connected
tunnel. A plugin call is not image delivery. Native Codex delegation remains
disabled until native actions, local approval, interruption and images pass on
that platform. No Cua substitution, hidden approval or unknown-action replay.

## Historical failures to regression coverage

| History | Required check / regression |
|---|---|
| Mac installed path differs from permission host | Fixed `/Applications/CuaDriver.app`; stable activity app; native startup check |
| Archive hash matches but signature fails | Separate archive digest and native codesign checks; stop on failure |
| Permission unknown / input monitor absent | Report unknown as unavailable; never infer granted from process existence |
| Tunnel lost when terminal closes | Detached user-session owner, autostart, backoff and ready endpoint |
| `driver_unavailable` after cancelled read | Safe diagnostic categories, reconnect only between calls, invalidate observations |
| Pause mistaken for outage | Distinct paused/connected state; repair never resumes |
| DNS, TLS and sandbox network denial conflated | Separate diagnostic categories; no proxy/DNS/TLS configuration changes |
| Missing or unchanged expect | New observed postcondition guidance; immutable unknown receipts |
| Python 3.9 and absent Library interface | No legacy Python uploader; native MCP images and actual host attachment capabilities |
| Multiple installs / upgrade interruption | Discovery, explicit instance selection, staged versions and pointer rollback |
| Mac OS `/var` and `/tmp` temporary aliases rejected | Normalize only verified system aliases; native install and containment regressions |
| Inline codesign requirement mistaken for a file | Prefix requirement with `=`; exercise real codesign parser in Mac CI |
| Race-instrumented synthetic process slow to exit | Bounded graceful-exit fixture budget and explicit child exit errors |
| Native CU inferred from app-server availability | Actual native capability gate, blocked reason exposed to Dot |
| 0.1 idle agent reported as an active task during upgrade | Lifetime lock checked separately; authenticated device identity before shutdown; unsupported endpoint reported as requiring normal local exit; never delete the lock |
| Startup apps probe hides failure after closing driver | Retain sanitized operation/category before cleanup; distinguish apps, windows, deadline and transport errors; do not expose native response content |

The last two regression fixes are in source after alpha.1; the published alpha.1
installation assets have not been replaced. A user reported that exiting the old
Mac agent and retrying completed migration. A fresh read-only plugin call confirmed
the installed version as `0.2.0-alpha.1` and the retained device identity. The agent
was paused, activity detection unavailable, and apps returned `driver_unavailable`.
This is migration/connection evidence only; the Mac desktop and screenshot gates
remain open. The old installer message does not prove an active desktop task.

Private logs, screenshots, credentials and local machine identifiers are not
release assets. Third-party executables remain unmodified and retain licenses.
