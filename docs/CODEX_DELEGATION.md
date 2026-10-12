# Target Codex delegation (candidate implementation)

DotControl can now run an owned official Codex task through the public App Server
stdio protocol, continue it, collect events and native MCP images, and request
interruption. This implementation is being validated; **the packaged application's
platform acceptance table remains closed**. A configuration profile or successful
tool inventory does not enable delegation. The existing Cua route is separate.

See [the earlier evidence](validation/target-codex-progress-2026-10-12.json) and
[the native/SSH follow-up](validation/native-ssh-progress-2026-10-12.json) for real
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
   Visible requests the public `codex://threads/<id>` link after the turn ends and
   the official connection and device lease have been cleaned up. Cancellation
   does not open a task window.
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

To authorize a selected public key, save the server profile, select its public-key
file, inspect the server and key fingerprint, then choose **打开 SSH 窗口**. Normal
OpenSSH owns the local hidden password/passphrase prompt and host-key confirmation.
The remote authorization command appends only a missing key, preserves existing
restrictions, and refuses unexpected links, ownership or permissions. It does not
modify sshd. A fresh operation receipt and a second strict connection using the
selected key must both pass. Exit code zero alone is insufficient. The wizard
distinguishes connection-only success from verified remote-directory access.

An interrupted authorization remains unconfirmed. Its bounded diagnostic records
only a failure category; raw SSH diagnostics are shown exclusively in the local
terminal. A connection closing near a password prompt does not establish whether
the password was correct. No deployment follows a failed authentication check.

## Remote tools

The original six desktop tools remain available. These five manage only tasks
created by this DotControl device:

| Tool | Required inputs | Behavior |
| --- | --- | --- |
| `codex_submit` | device_id, request_id, prompt, profile_id | Records a job and returns immediately. Optional server_profile_id, experiment_id and presentation. An experiment requires an existing local server profile. |
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

An interrupted terminal task may receive a new explicit continuation only after
the bound official turn has a durable `interrupted` receipt and its connection
has finished closing. It retains the same thread and experiment binding, appends
a new request intent and obtains fresh approvals. Unknown outcomes, native desktop
cancellations, pre-dispatch cancellation and historical blocked records cannot
use this path. A failed intent write cannot clear the cancellation state.

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

The protected local wizard lists only DotControl-owned tasks. Terminal approvals
can be answered there. A desktop approval instead offers **停止远程执行并在 Codex
接手**: DotControl interrupts, waits for official stop evidence, cleans up, and
opens that owned thread. It consumes the old approval, including in-flight human
answers, rather than transferring it into a new turn. Unconfirmed stopping retains
the desktop guard and does not open a competing executor. This local handoff is
reported separately and does not count as Mac remote approval acceptance.

The local page shows each approval's deadline and a receipt beside its controls.
After the list is opened, read-only refreshes are serialized and preserve entered
fields. Expired, consumed, missing or terminal requests disable the expanded form
as well as its list button. A failed answer response remains uncertain and is not
automatically resent. None of these UI refreshes extends an approval's lifetime.

The release gate for remote review remains closed until the actual source Mac Dot
and Secure MCP Tunnel display accept, reject and cancel correctly. Automated HTTP
MCP tests do not establish this host compatibility. DotControl never treats the
model's words or a remote task prompt as the human's approval.

## Coordination, durability and results

Cua and native desktop jobs share one device lease. Native dispatch requires
available activity detection, an unlocked desktop and 60 seconds of idle time.
An active user puts the job in cancellable `waiting_for_idle`; no desktop input
or exclusive reservation is made during that wait. Pause, lock and monitor loss
stop dispatch. The complete safety check runs again when acquiring the lease.
Human input or local pause revokes the lease and requests official interruption.
A successful interrupt RPC is not stop confirmation. If stop is uncertain, a
durable guard prevents another desktop executor from acquiring the device, even
after restart. Fresh observation is required after delegated input.

Windows native input has an additional unresolved acceptance constraint. Its
conservative monitor treats every changed `GetLastInputInfo` timestamp as
activity, including injected input, and the native lease revokes on any activity
generation change. Thus an official action using `SendInput` is expected to
interrupt its own task. This is a code-level finding, not a completed physical
desktop test; UI Automation actions may behave differently. Removing that
fallback or ignoring all injected input would weaken touch, pen and accessibility
takeover detection, so this implementation does neither. Native delegation stays
closed pending a supported way to distinguish executor input or an equivalently
verified official takeover mechanism, plus real multi-action/interruption tests.
See Microsoft's [input timestamp contract](https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-getlastinputinfo).

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

`review_location` identifies a host form or required target-local handling.
`image_delivery_status=received_in_band` means official image bytes were acquired;
it does not establish that source Dot rendered them. Restart reports lost in-memory
image data as `unavailable_after_restart`.

Model text alone leaves task verification `unverified`. The fixed fixture can
become `verified_fixture` only through the independent SSH readback described
below. Native image return does not prove source rendering.

## SSH experiment acceptance procedure

Choose a server profile locally and get its read-only preflight to pass first.
For the first acceptance, submit `experiment_id` with that saved profile. The
bundled helper supports only the fixed sum `1...1,000,000 = 500000500000`, using
one AWK process on a Linux server, no GPU and no dependency installation. It does
not execute a configured arbitrary deployment command. General deployments need
their own reviewed job adapter; a prompt alone does not provide deduplication.

The target Codex receives exact helper argument arrays for `experiment status`,
`submit` and explicit `stop`, then invokes them through its official terminal tool.
The controller freezes the accepted server/fixture binding before dispatch. The
helper checks that record even if an invocation omits its expected-binding flag;
editing the profile mid-task cannot redirect the experiment or verify another
server's output. A separate durable intent permits only one launch attempt.

Remote atomic directory creation claims `<remote_dir>/dotcontrol-<experiment_id>`.
The detached worker records its process identity, log, exit code and result hash.
An independent SSH query checks the actual output bytes, exit code and SHA-256.
`runtime.experiment` and `verification=verified_fixture` describe that evidence,
separately from completion of the Codex turn. An ambiguous attempt becomes
query-only and must not be replayed with a fresh ID. Existing legacy unknown
records retain that meaning after upgrade.

`experiment stop` writes a cooperative request only for an owned running fixture
whose recorded identity still matches. It never signals arbitrary processes;
`stop_requested` still needs a later terminal-state check. Cancelling the Codex
control task does not call stop. See the [helper contract](../internal/experiment/README.md).

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
