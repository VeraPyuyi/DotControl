# Codex delegation in DotControl 0.2

This beta ships an owned job contract, a durable private job journal, and a read-only client for the official Codex App Server over STDIO. **Native Codex desktop delegation is blocked in this release.** Installing Codex, finding the Computer Use skill, or completing a model answer does not enable execution. There is no JSON setting, environment switch, or configuration flag that marks native acceptance as passed.

The validated feasibility record is [codex-app-server-2026-10-12.json](validation/codex-app-server-2026-10-12.json). The current app-bundled official binary supported initialization, an isolated model turn, the official `@oai/sky` import, and native `sky.list_apps` discovery through public RPC. The model turn did not invoke native tools. Native screenshot delivery, a delegated desktop action, target-local human approval, and in-flight cancellation remain unverified. This is evidence of a partial native route, not evidence that native tools are absent or that remote execution is accepted.

## Public tools

`devices` reports four separate capability objects. Each includes `available`, `state`, and a stable `code` where relevant:

| Capability | Meaning in this beta |
| --- | --- |
| `base` | Basic DotControl driver readiness. Defaults to `not_probed`; the local runtime can supply transport status. Pause, activity-monitor failure, and locked desktop are explicit blockers. Activity alone does not establish driver readiness. |
| `codex_connection` | The owned bridge starts no persistent Codex process, so device discovery reports `not_probed`. A separate `codex-probe` can verify the public handshake at its `checked_at` time. |
| `official_computer_use` | `not_verified`, with `official_cu_acceptance_required`. Inventory is not native acceptance. |
| `codex_delegation` | `blocked`, with `official_cu_acceptance_required`. No desktop action is started. |

### `codex_submit`

```json
{
  "device_id": "<exact authorized device_id>",
  "request_id": "request-stable-001",
  "prompt": "<bounded task description>",
  "allowed_apps": ["<exact allowed app identity>"],
  "working_dirs": ["<absolute target-device directory>"]
}
```

Returns an immediate durable `job_id`. The valid request returns `state: blocked`, `code: official_cu_acceptance_required`, with a fixed explanation of the outstanding acceptance work. It creates no Codex thread or desktop action. A request with a valid stable ID but an invalid prompt or scope returns a persisted `rejected` job. Invalid device ownership or invalid request-ID syntax is rejected before a job is allocated.

The prompt must be nonempty and at most 65,536 bytes. Both scope lists must contain 1–64 entries. App identifiers are bounded strings without wildcards or line breaks. Working directories must be absolute on the target device and contain no wildcards or line breaks. These are admission checks for the disabled contract, not an implemented desktop or filesystem enforcement boundary.

An identical decoded request body with the same `request_id` returns its existing job, including after restart or cancellation. A changed body under that ID returns `request_id_conflict`; it never replaces the existing job. Array order and exact strings are significant. Request and device IDs use 1–128 ASCII letters, digits, `.`, `_`, `:`, or `-`, beginning with a letter or digit. Stable IDs are identifiers, not containers for task content. Never resend an uncertain effect with a new request ID.

### `codex_job` and `codex_cancel`

```json
{"device_id": "<exact authorized device_id>", "job_id": "<returned owned job_id>"}
```

`codex_job` reads only this store's jobs. `codex_cancel` durably sets `cancel_requested: true`; repeated cancellation returns the same record. Because `blocked` and `rejected` are terminal, cancellation preserves their original state and reason. It does not claim that native work was interrupted. An unknown ID returns `job_not_found`; a different device returns `wrong_device`. Codex thread IDs cannot be substituted for job IDs.

With no bridge configured, all three methods return the structured `unsupported` result with code `codex_bridge_not_configured`. They do not silently fall back to a third-party desktop provider.

## Storage and ownership

`codex-jobs.jsonl`, `codex-jobs.key`, and `codex-jobs.lock` belong to DotControl's local state directory. The directory is private to the operating-system user (a protected current-user ACL on Windows, mode 0700 elsewhere). A process lock prevents competing writers. Every record is flushed before acknowledgment; malformed, truncated, mismatched-device, or broken-chain journals fail closed. Storage errors do not return an accepted job. Once an append fails, that store refuses further operations until restarted and verified.

Jobs persist only owned IDs, fixed status text, timestamps, cancellation intent, and HMAC digests. The prompt, app allowlist, working directory list, screenshots, and model text are not stored. Prompt and request digests use a random 32-byte per-store HMAC key, not an unsalted prompt hash. A chained MAC detects modification and reordering of retained records. It is not rollback protection against a local owner who can replace the entire directory or truncate a complete suffix. Local state access remains the operating-system security boundary.

All peers authorized for a device use that device's DotControl-owned namespace; this is not per-remote-caller tenancy. The store does not enumerate, import, resume, cancel, or inspect the user's unrelated Codex threads. No actual Codex thread IDs exist in these blocked records.

## Official STDIO probe and protocol client

The runtime selects an explicitly installed official executable, then calls `codexbridge.Probe` with its absolute path and an absolute working directory. The probe launches a dedicated hidden `codex app-server --stdio` process. It reuses existing Codex login/settings through Codex itself and neither parses authentication files nor changes global configuration. DotControl exposes no App Server port and never attaches to a user's existing daemon.

The probe sends only:

1. `initialize`, identifying DotControl and enabling the documented experimental protocol capability.
2. The `initialized` notification.
3. `skills/list` for the supplied directory.
4. `mcpServerStatus/list`, with bounded pagination.

It starts no thread or model turn, calls no MCP tool or native input API, and answers no approval. It returns booleans about the enabled Computer Use skill and listed `node_repl.js`/`cua_repl.js` tools without returning private skill paths, tool schemas, or inventory content. A false inventory boolean means “not listed in this probe,” not “native capability is absent.” Some plugin inventories are thread-specific. `connection.state: probe_passed` records a successful handshake at `checked_at`; the dedicated process closes when the probe completes. Even a complete inventory leaves delegation blocked.

The internal protocol client handles responses, notifications, and server-initiated requests. It sends each RPC once without automatic replay. Loss or timeout after a request may have been sent produces a `TransportError` with `OutcomeUnknown: true`; a pre-send cancellation is distinguishable. Raw upstream error text and process stderr are not exposed because they may include private content. Pending approval, elicitation, and user-input requests remain in a bounded memory queue. No automatic acceptance or approval-response API exists in this release. Queue overflow fails closed instead of dropping an approval or silently proceeding. The arbitrary RPC primitive is internal and is not a public DotControl method.

The beta uses the official public process/protocol only. It does not copy proprietary runtime components, directly launch native helper binaries, use private protocols, or install a replacement native backend.

## Requirements before execution can be enabled

A future implementation must first verify native image delivery, actual delegated native action, local human approval, and interruption/unknown outcomes on the exact supported version and OS. It must enforce allowed apps and working directories; prompt instructions alone are insufficient. It must obtain the existing engine's sole device execution gate for the full delegated job, preserve the 60-second human-idle threshold, immediately respect human activity and pause, and invalidate observations after interruption. This beta deliberately adds no competing input lease because no Codex job executes.

Target-local approval must be reviewable by the human on that device. A remote task request cannot act as an approval, and a background responder must never auto-accept it. Cancellation needs confirmed `turn/interrupt` behavior for actual delegated native work; stopping a process or setting a flag does not prove that an already-issued desktop action had no effect. Reconnection must reconcile only owned jobs and preserve unknown outcomes without replay. Execution cannot be enabled by an inventory report or an editable “passed” flag.

## Verification

Automated tests cover concurrent request deduplication, conflicting bodies, restart recovery, input privacy, cross-device/foreign-ID rejection, process locking, corrupt journals, protocol framing, no retry after disconnect, timeout uncertainty, pending approvals without automatic responses, read-only probe sequencing, service routing, and MCP in-memory/HTTP argument round trips. These checks validate the blocked beta contract and transport behavior; they are not native desktop acceptance.
