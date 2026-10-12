# Target Codex delegation (candidate implementation)

DotControl can now run an owned official Codex task through the public App Server
stdio protocol, continue it, collect events and native MCP images, and request
interruption. This implementation is being validated; **the packaged application's
platform acceptance table remains closed**. A configuration profile or successful
tool inventory does not enable delegation. The existing Cua route is separate.

See [the dated evidence](validation/target-codex-progress-2026-10-12.json) for real
tests versus automated tests. Windows has passed persistent official thread
visibility, background continuation, a real terminal CPU check, request deduplication
and a cancelled turn. Native app discovery and a real official application approval
request were observed. A delegated GUI action, screenshot, Mac human review over
the tunnel, and authenticated server experiment have not passed.

## Local configuration

Open the installed agent's **onboard** page and choose “连接本机 Codex”:

1. Discover the program bundled with the running official desktop application.
   Save its reference. The execution adapter accepts the explicitly tested version
   `0.162.0-alpha.17.2`; other versions report `codex_version_not_accepted`.
2. Select a project directory, read its official permission profiles, and choose
   an existing profile. Built-in IDs include `:read-only` and `:workspace`;
   their leading colon is significant. DotControl does not alter global permissions.
3. Choose terminal or Computer Use and visible or background presentation.
   Visible requests the public `codex://threads/<id>` link after creating the thread.
   `open_requested` means the OS accepted the link, not that a window was observed.
   Background sends no open request. Computer Use on Windows still uses a foreground,
   unlocked desktop, even with background presentation.
4. Optionally save one or more named server profiles: SSH alias or user@host, port,
   remote directory, optional private-key **file reference**, and optional deployment
   command. Selecting a file does not read its contents. Save does not deploy.

The target's official Codex process reads its own login and model settings.
DotControl does not copy credentials. Delegated turns consume that account's quota.
Terminal profiles disable Computer Use in the spawned official process. Selected
application names and project/deployment instructions are task scope; they are not
an OS sandbox or a guarantee against every command that the selected official
permission profile permits. Choose the least permissions appropriate to the task.

The protected loopback wizard owns configuration. Local directories, server
endpoints, deployment commands and key paths are not included in diagnostics or
the remote profile catalog. `devices.codex_profiles` exposes only chosen profile
names, IDs, execution kind and presentation so source Dot can select a saved profile.
Do not put secrets in profile names.

SSH preflight uses the target's normal SSH configuration and agent with strict
host verification and batch authentication. It checks connectivity and optionally
directory access, without running a deployment command. Unknown or changed host
keys require the local user's verification. Authentication failure requires local
SSH setup or unlocking the agent. Passwords, passphrases, key bodies and tunnel
keys must never be pasted into chat, configuration JSON, command arguments or logs.
The local wizard contains no SSH password field and does not automate password login.

## Remote tools

The original six desktop tools remain available. These five manage only tasks
created by this DotControl device:

| Tool | Required inputs | Behavior |
| --- | --- | --- |
| `codex_submit` | device_id, request_id, prompt, profile_id | Records a job and returns immediately. Optional server_profile_id and presentation. |
| `codex_job` | device_id, job_id | Returns current phase, owned official thread/turn IDs, pending review, bounded results and MCP images. |
| `codex_continue` | device_id, job_id, request_id, prompt | Appends a new turn to that owned thread, when no other turn is active. |
| `codex_cancel` | device_id, job_id | Persists cancellation and requests interruption; waits for official terminal evidence before confirming stop. |
| `codex_review` | device_id, job_id, approval_id | Opens the host's human form, without any model-supplied approval argument. |

Example submission (fictional configuration IDs):

```json
{
  "device_id": "<device returned by devices>",
  "request_id": "experiment-cpu-001",
  "profile_id": "configured-terminal-profile",
  "server_profile_id": "configured-test-server",
  "presentation": "background",
  "prompt": "Check the selected server and report readiness. Do not start the experiment yet."
}
```

Profile-based requests cannot override local app/directory scopes. Reusing the
same request ID and identical decoded body returns the existing job. Different
content under the same ID is a conflict. Continue IDs use the same per-device
namespace. A profile change blocks continuation until the user chooses a new task.
Legacy requests without a profile retain the old blocked contract and journal.

Only the official thread recorded for an owned job is used. No arbitrary thread
resume or RPC is exposed. A desktop active-writer response becomes `local_takeover`;
DotControl does not force takeover or submit concurrently. The user can continue
locally. Remote continuation requires that the official writer is released.

## Human review

For supported command/file/permission/MCP approval events, the job exposes a
bounded review record bound to device, job, official thread, turn and server request.
`codex_review` requests a native host form through negotiated MCP input requests.
There is no `accept` or decision argument in its tool schema. The returned human
response carries a random, expiring, single-use challenge bound to that exact review.

A host without form support keeps the job waiting. Secret-entry forms, persistent
grants and unsupported official requests remain target-local interactions.
The gateway does not forward an approval callback. Cancellation, expiry, resolved
requests, new turns, local pause and revoked desktop leases invalidate review.
A write failure is uncertain and is not retried automatically.

The release gate for remote review remains closed until the actual source Mac Dot
and Secure MCP Tunnel display accept, reject and cancel correctly. Automated HTTP
MCP tests do not establish this host compatibility. DotControl never treats the
model's words or a remote task prompt as the human's approval.

## Coordination, durability and results

Cua and native desktop jobs share one device lease. Native dispatch requires
available activity detection, an unlocked desktop and 60 seconds of idle time.
Human input or local pause revokes the lease and requests official interruption.
A successful interrupt RPC is not stop confirmation. If stop is uncertain, a
durable guard prevents another desktop executor from acquiring the device, even
after restart. Fresh observation is required after delegated input.

Terminal tasks do not acquire the desktop lease; they still respect local pause.
Cancelling a Codex control task does **not** mean an independently submitted server
experiment was killed. A server stop is a separate explicit operation.

New runtime records use `codex-runtime-v1.jsonl`, chained and authenticated with the
device's existing private journal key. The old journal remains intact. Intent is
persisted before thread/turn dispatch. Uncertain thread creation or turn dispatch
is never replayed. Recovery reads only the recorded owned thread and exact turn.
A lost turn ID cannot be reconstructed by guessing from message order.

The journal excludes prompts, text outputs, screenshots and approval contents.
Official Codex may keep its own task history. Runtime results are bounded in memory;
after restart their status is `unavailable_after_restart` unless restored from actual
evidence. Native image content is returned separately from structured JSON; no
legacy Python Library upload script is used.

These are independent facts:

- A Codex turn reached a terminal state.
- Command/tool output or another independent artifact verifies the requested result.
- MCP image content was returned.
- The source Mac actually displayed the image.

The bridge leaves task verification `unverified`; a model saying “done” does not
verify an experiment. Native image return does not prove source rendering.

## SSH experiment acceptance procedure

Choose a server profile locally and get its read-only preflight to pass first.
Then let target Codex check the selected project, remote directory, resource needs
and intended deployment. Use a stable experiment ID and an independent experiment
directory. Record a durable remote submission marker before starting work, plus
a scheduler job ID or PID with logs and output paths. On disconnection, query the
same record and remote job before doing anything else. An ambiguous submission
must not be retried under a new ID. Generic deployment instructions do not enforce
remote exactly-once execution; the selected scheduler or deployment script must
implement its own idempotency. This remote experiment behavior remains unaccepted.

## Validation and limits

Automated tests exercise durable deduplication, ownership, transport uncertainty,
profile privacy, permission IDs, host-form review, cancellation races, restart
guards, image separation and the shared desktop lease. Platform compilation and
mock web rendering are recorded separately from installed device tests.

Unverified gates are compiled closed, with no environment/configuration bypass.
The adapter uses public [App Server](https://learn.chatgpt.com/docs/app-server)
stdio only, the documented [thread deep link](https://learn.chatgpt.com/docs/reference/commands),
and the target's own official components. It exposes no App Server network port,
uses no private helper protocol and redistributes no proprietary Computer Use binary.
