# Mac installer startup and diagnostic repair

Base: `a5d66d0747447982945b05b91c66953c02bf5cfd` (`main`). The initial native diagnostic patch was prepared against alpha.2 source `ec65d7275a816398a91397d53c89b6a5edf38336`.

## Observed failures and scope

The Mac connected through its plugin and delivered a Calculator window image after manual service registration and permission repair. This proves the manually repaired connection; it does not prove a clean installation of these new bytes.

The installer wrote a LaunchAgent definition but did not bootstrap it. Installation, rollback, and reopening onboarding also started detached agents directly. A plist file therefore appeared as enabled even when launchd had no registered service. The manually started helper could be attributed to Terminal for input monitoring. Activity `available=false` additionally covered helper and sampling failures, while onboarding called every such failure missing permission.

The page kept old status after its local session expired and left old polling active. Image observation and human confirmation were separate evidence, but the confirmation control was away from delivery status and the permissions row continued to request image verification after confirmation.

One controlled `list_apps` failure was recorded at **2026-10-12T06:21:08.146894Z**. The local diagnostic identified operation `list_apps`, category `tool_error`. The source client's visible fields were `structuredContent.error_code = "INVALID_ARGUMENT"` and `content[0].text = "driver_unavailable"`. The original record exposed no native CuaDriver code or JSON-RPC code/message. These fields are kept distinct; the client wrapper is not evidence of an argument-validation cause in CuaDriver. The intermittent native root cause remains unconfirmed.

The Windows project is independently developing official Codex delegation, approval and SSH acceptance. This branch incorporates no changes from that working tree. The alpha.2 per-connection Cua session fix is already in the base and is not reintroduced here.

## Implemented behavior

- Installation and rollback activate the owned `gui/<uid>/org.dotcontrol.agent` LaunchAgent. Reopening onboarding uses that same job. Ownership checks include the definition's user, label, program arguments, state directory, and loaded program/path. An existing unmanaged instance or an unrelated job is refused. Lifecycle changes serialize on a private lock; existing engine lifetime locking and authenticated graceful shutdown remain in force. User-disabled login jobs are refused before stopping the old agent or activating an upgrade; they are never automatically enabled.
- The job associates `org.dotcontrol.menu`, the installed **DotControl.app**. Owned agent, activity, menu and setup builds have explicit signing identifiers, and payload hashes are generated after signing. Upstream CuaDriver is only signature-verified; it is never re-signed. [Apple's responsible-code guidance](https://developer.apple.com/forums/thread/678819) motivates the association. These are still ad-hoc signatures: explicit identifiers alone do not guarantee recognition across upgrades. Fresh native permission attribution remains an acceptance requirement.
- Local status adds `autostart_status` and `activity_status`. Activity reasons distinguish permission missing, helper unavailable, waiting/stale/invalid sample, and absent foreground app. Only fixed categories and permission booleans are accepted from the helper; no key contents or app names are added. TCC preflight is sampled once per second, independently of hardware activity sampling.
- The adapter captures allowlisted native code, stage, class, origin, time and classification source before wrapping. Fixed-template hints are explicitly marked as hints. Unknown, conflicting, oversized, duplicate-key and private-container metadata fail closed. Cleanup does not replace the failed request, and runtime status retains recent failure across connection replacement while separately showing current connection health.
- `serve --native-failure-log <path>` explicitly enables the append-and-sync journal. Unix validates a single-link user-owned regular file with private permission bits and refuses final symlinks. Windows creates a protected current-user-only ACL, validates it by handle, refuses reparse points/hardlinks/alternate streams, and never changes an existing public file's ACL. Default installation does not enable a journal. MCP tools and external error fields are unchanged.
- Onboarding clears stale evidence and runtime-key input on auth/CSRF expiry, aborts pending requests, stops polling and asks the user to reopen it. A transient connection failure clears stale status but keeps authenticated polling available for recovery. Sequence/epoch checks reject old responses. Human image confirmation is beside delivery status; confirmed images also update the permission wording. Confirmation, pause and revoke rules remain unchanged.

## Verification

Local Apple Silicon checks passed: full Go tests, full race checks, `go vet`, Windows amd64 and Mac Intel cross-builds, the frontend regression suite, and portable packaging/signature tests. A fresh owned-component build verified all four identifiers and seven signed-payload file hashes without installing or launching it. Local tests use temporary definitions and fake launchctl; they do not perform installation/switch tests against the user's service.

CI runs on Windows, Mac ARM and Mac Intel and executes full tests, race checks, vet, agent build, frontend tests and packaging tests. Mac jobs also build new owned components and verify signature identifiers and hashes. CI results for the PR head belong in the PR's checks; this record does not substitute mocked or cross-compiled results for native Windows execution.

Still required before a release: a new-install/upgrade/rollback acceptance run with the new installer on ARM and Intel, including real login registration, user-disabled startup, permission attribution to the installed app, successful source Dot observation, and the user's confirmation of visible image delivery. No service switch, permission change, CuaDriver signature change, installer publication, or Windows working-tree merge was performed for this PR.
