# macOS lifecycle and internal pairing preparation

This work builds on main `c90155c`; it is a local source change, not a new release or signed installer. The official private plugin and tunnel remain the supported connection path. There is no plugin-free control mode, public relay, cross-network transport, controller UI, or new ChatGPT login requirement.

## Startup and permissions

A saved LaunchAgent definition, a registered service, and a running process are separate evidence. A matching loaded service needs a positive PID before the cached runtime status can say it is managed. An explicitly disabled service is rejected before any kickstart, even if it remains registered. Conflicting executable/state paths or an unknown loaded definition are refused.

For `managed_startup_disabled`, inspect the current user's DotControl login/background setting and enable it only if you want background operation, then reopen the installed app. For `managed_startup_unavailable` or `launch_agent_loaded_definition_unknown`, use the installer to repair the selected installation; do not manually overwrite another instance's plist. A saved login plist describes the login preference; it does not prove that launchd currently runs the service.

Diagnostic downloads now include `activity_status`. `helper_unavailable` means the helper did not start or its output ended; `awaiting_sample`, `sample_stale`, `invalid_sample`, and `no_foreground_app` describe sampling failures. Only explicit helper permission evidence reports `permission_missing`. An unavailable activity stream must not be interpreted as user inactivity. macOS Input Monitoring belongs to the installed DotControlActivity helper; Cua Accessibility and Screen Recording are separate permissions.

## Installation recovery

The existing installation transaction remains authoritative. It suspends automatic restart before waiting for the old process, restores configuration/startup state on failed activation, and verifies health before committing. Old-version cleanup checks recorded ownership, file hashes, and process/startup references. Unknown or modified files remain untouched.

Device identity, pause/revocation state, history sharing scope, and unrelated configuration survive reinstall and interrupted upgrades. A genuinely missing old executable cannot be restarted: recovery explicitly reports `rollback_previous_program_unavailable`, preserves configuration, and allows a subsequent repair. This is not a successful rollback-to-running claim.

## Internal one-time invitation API

The bridge service has internal `CreateInvitation(deviceID, ttl)`, `ConsumeInvitation(token, deviceID)`, and `RevokeInvitation(token)` methods. They are not exposed through HTTP, CLI, or the onboarding UI.

- Invitations use 32 cryptographically random bytes, returned as 64 hexadecimal characters. The private state file stores only their hashes.
- The device ID is bound at creation. TTL must be positive and at most 15 minutes. A new invitation replaces an outstanding invitation for the same device; at most 128 active invitations are retained.
- Consumption checks device and expiry under the service mutex, creates an initially unprivileged client identity, and removes the invitation in one private state-file replacement. Reuse and concurrent second consumption fail.
- Pending invitations and paired identities survive service restart. Explicit revocation deletes pending authorization; existing `RevokeClient` revokes a paired bearer credential.
- The consumer receives the durable client's bearer credential once and must retain it securely. If the response is lost after persistence, create a new device identity through a future operator flow; retrying an already consumed invitation never returns the secret again.
- This follows the existing single-service-owner model. Independent concurrent processes writing the same bridge state are not supported. Local same-user malicious processes are outside the private-file isolation boundary.

## Acceptance limits

Windows fixtures and Go race tests do not prove macOS launchd/TCC behavior. macOS Swift compilation and real startup/permission behavior, Developer ID signing/notarization, original source Dot image display, and execution on the user's Mac remain pending. No driver is installed or launched and no system login item is changed by this implementation session.
