---
name: connect-dotcontrol
description: Use when the user asks to install, connect, repair, or diagnose DotControl, or mentions a target DotControl Windows or Mac plugin to connect or use that computer's Codex.
---

# Install and connect DotControl

Use the same released installer as the graphical setup. Do not implement an
alternate installer, compile components, install Python/Homebrew/Xcode/Go,
edit credential stores directly, or alter system DNS/proxy/TLS settings. Use only
the protected local wizard for DotControl's own network mode; it does not change
system settings.

## Discover and install

For an already configured remote target, follow **Connect to a mentioned target**
first. A remote mention is not a request to install on the source computer.

1. Identify the local OS/architecture using normal local tools. Windows supports
   x64; macOS supports arm64 and x86_64. Do not switch Dot's attached computer.
2. Check the existing DotControl state location (Windows `%APPDATA%/DotControl`,
   Mac `~/Library/Application Support/DotControl`) and installed program record.
   Read only nonsecret configuration and installation records. Never read vault
   values, authentication files, screenshots, or action ledgers for installation.
3. If installed, use its `onboard` or `repair` command. If multiple instances are
   present, show their names and use the user's selected instance; never invent
   a fresh identity as a repair. On Windows installed versions are under
   `%LOCALAPPDATA%/DotControl`; Mac under the state directory's `program` folder.
   `current.json` identifies the installed version directory. Verify the selected
   path stays beneath this root before executing.
4. Otherwise download the appropriate asset and `.sha256` file from the fixed
   release `https://github.com/VeraPyuyi/DotControl/releases/tag/v0.2.0-alpha.7`.
   Asset names: `DotControl-0.2.0-alpha.7-windows-amd64-setup.exe`,
   `DotControl-0.2.0-alpha.7-macos-arm64.dmg`, or
   `DotControl-0.2.0-alpha.7-macos-amd64.dmg`.
   Verify SHA-256 using built-in OS facilities before launching. Do not substitute
   another tag or ignore mismatched/missing assets. A checksum protects download
   integrity; these alpha installers are not publisher-signed/notarized.
5. Launch the installer using normal OS tools and follow its local page. On Mac,
   mount the DMG and open `DotControl Setup.app`. The user must handle any OS
   confirmation. Never remove quarantine, disable Gatekeeper, re-sign CuaDriver,
   or override a signature failure. Report the specific gate instead.

## Connect to a mentioned target

When the user sends a message mentioning a target DotControl plugin, call that
entry's `devices` first. Keep source Dot attached to its original computer;
do not switch official attachment, create a target Dot conversation, or assume
that equally named plugins share a device directory. Check the returned ID,
fresh nonce, pause/revocation and relevant terminal/history/desktop capability.

A connection-only request ends with that read-only result. It does not authorize
a test task, window inspection, permission enabling or history-scope changes.
For a requested task, first check that its required tools exist, then use the
locally saved target permissions. Missing history access is not permission to
read files through a terminal task. A missing terminal capability is not a reason
to try `execute` or attach the target as the official source computer.

On a transient connection error, at most one additional read-only `devices`
check may be made after 5 seconds. No successful response means connection is
unverified. `tunnel_client_not_seen`, offline, paused, revoked, missing login and
unsupported capabilities are different blockers. Do not retry a refused action.
After an uncertain submission, query the existing job using its stable identity;
never create a replacement. Report exact returned evidence without exposing secrets.

Mentioning a plugin is model guidance, not an OS wake hook. The target must be
awake, logged in and running its agent/tunnel. Login startup and OS-managed crash
recovery maintain this receiver. Closing the console leaves it running; explicit
Quit keeps it stopped until local reopening or the next enabled login startup.
If the target receiver is stopped, explain target-local startup is needed; never
launch a source-computer agent and claim the target recovered. Remote calls cannot
turn on, wake or log in a target. Repair/restart never expands permissions.

## Official webpage assistance

Use only the host's normal supported browser tools. No private endpoints,
cookie extraction, hidden browser debugging ports, or account credential copies.
If browser assistance is unavailable, the local wizard has the exact links and
copyable fields; leave it open for the user.

1. Open `https://platform.openai.com/settings/organization/tunnels`. Use a unique
   tunnel for this device and the user's intended ChatGPT workspace. Existing
   device configuration takes precedence. Never reuse another device's tunnel.
2. Open `https://platform.openai.com/settings/organization/api-keys`. Guide the
   user to a runtime key with only Tunnels Read + Use. Do not request an admin key.
   The user enters the runtime key in DotControl's local password field. Never
   ask for it in chat, retrieve it through browser snapshots, type it into a tool
   argument, or pass it on the command line. Wait for the local vault check.
3. Open `https://chatgpt.com/plugins`, add a custom MCP server, select Tunnel,
   and fill the device name and exact tunnel ID shown in the local wizard.
   Official login, risk acknowledgements and permissions belong to the user.
   Existing explicit authorization remains valid; don't repeat permission asks.
4. Do not call a private API if an official form lacks automation. Explain the
   one required local step and continue other independent checks.

## Verify and enable

Keep these checks separate: installed; local agent healthy; driver usable;
activity detection available; current-process cloud poll confirmed; source Dot plugin devices call;
MCP image returned; image actually visible at source Dot. Never substitute
process existence for readiness or local image viewing for cloud delivery.

The default network mode is `auto`: proxy environment applicable to the HTTPS
control service first, then the current user's applicable static system proxy,
honoring bypass rules. Environment precedence is `DOTCONTROL_CONTROL_PROXY`,
`CONTROL_PLANE_HTTP_PROXY`, `HTTPS_PROXY` / `https_proxy`, then `ALL_PROXY` /
`all_proxy`. Do not fall back to `HTTP_PROXY` / `http_proxy` or HTTP-only system
settings for this HTTPS target. Windows can use an HTTPS-specific or
protocol-neutral static proxy; Mac uses the effective HTTPS static proxy.
The proxy endpoint itself may use either HTTP or HTTPS.
The local wizard offers auto, direct and manual HTTP/HTTPS modes. Manual addresses
must not contain usernames or passwords. Only the tunnel control plane uses this
route, through an environment reference; loopback services always connect directly.
Do not read proxy credentials, reproduce environment values, or change system
proxy/DNS/TLS settings. PAC-only, SOCKS-only and interactive proxy authentication
are explicit limitations. Report the returned reason and use local proxy handling
or an already available static HTTP/HTTPS route. Do not promise automatic PAC or
authentication support.

Saving a route returns `queued_until_restart`: “新的设置将在隧道下次启动时应用”.
Auto mode also detects changes periodically and marks `network_change_pending`.
The fixed official client 0.0.16 does not provide a lossless drain: a successful
poll can show idle before enqueueing work, and a graceful signal can cancel
active requests. Neither an idle snapshot nor zero counters permits a hot swap.
DotControl never terminates a live tunnel child for a proxy change. The latest
settings apply only after natural child exit and restart, or the next explicit
local restart. Pending may persist until then; saved is not connected. Do not
force a restart to clear this status, start a competing tunnel, replay work or
resume a paused/revoked device. Before an explicit local restart, confirm work
has finished and reconcile any uncertain results independently.

Cloud readiness requires local readiness AND a successful poll from the current
tunnel process, within `max(90 seconds, 2 * poll deadline)`, with no newer failure.
Detailed health must match the current process generation. A successful `/readyz`,
an existing process or `doctor` exit code alone does not establish cloud readiness.
In `doctor --online`, `official_tunnel_doctor=configuration_validated` means only
configuration was validated; `tunnel_cloud_connection` separately reads the
running supervisor's evidence. Missing or stale evidence stays unconfirmed.
The source must still check its current devices response and nonce; historical
`devices_seen` does not prove a new call succeeded. Keep the local service, cloud
poll and source invocation outcomes separate. No release, native-platform
or real-cloud acceptance should be claimed without matching current evidence.

The device starts paused. Only the local user may enable it in the wizard.
Repair, restart and upgrade preserve the existing pause/revoke state. On Mac,
open system settings for CuaDriver Accessibility/Screen Recording and
DotControlActivity Input Monitoring, then use Repair to recheck. Unknown
permissions are not granted permissions.

For an authorized desktop test, keep source Dot attached to its original computer and
call the target device's plugin. Get a fresh devices nonce, apps, then observe.
Use exactly one bounded action per new observation and stable action IDs.
Supply `expect` for a NEW verified visible state, retaining actual Unicode
direction marks. If an outcome is unknown, reobserve and reconcile without
repeating input or rewriting historical receipts. Verify final AX AND image.

Return native MCP image content through the host's actually available attachment
facility. Do not use legacy Python Library upload scripts or assume a tool exists.
If source image display is unconfirmed, report it separately and retain the task
result's actual status. Don't publish screenshots to unrelated cloud services.

## Native target Codex delegation

For basic installation, use the protected local **连接并启用 Codex** button.
It discovers the official executable and selects an independent task directory,
the official workspace profile and never-ask policy. It does not grant full access.
Do not require SSH configuration, public-key authorization, a server profile,
Computer Use or image delivery to finish basic installation. SSH and deployment
can be handled later in the target's own Codex task. Advanced settings are optional.

In terminal-only mode, FIRST inspect the tools actually available to the current
source conversation for the target plugin. Require `codex_submit`, `codex_job`,
`codex_continue` and `codex_cancel`. If any are missing, stop the test: use
**Refresh tools** in the source host's official plugin settings, then start a new
conversation and check again. Prompt for this refresh after upgrades. Local Codex
readiness, `devices_seen`, a successful devices call and capability `available`
do not verify the source tool catalog. This release exposes 13 tools; the old review
interface has been removed.

Only after all four tools are present, call the same plugin's `devices`, verify
the target device ID, inspect `capabilities.codex_terminal` and select a saved
terminal profile from `codex_profiles.execution`. Submit only when the capability
is available and a profile exists. Do not call apps/observe as a prerequisite,
and never put terminal commands into `execute`.
No driver, activity monitoring, idle time or desktop system permissions are needed.
Pause, revocation and locally selected permissions still apply. Unexpected human
requirements mean target-local handling; do not ask for repeated remote approvals.

Use the target's protected local **Codex** view to discover and save its official
program, choose a project directory, read existing permission profiles, and save a
basic terminal profile with the user-selected permissions and never-ask policy.
Preserve exact permission IDs, including leading colons. Do not silently convert
old on-request profiles: explain that they need explicit target-local configuration.
Visible/background only controls opening the official task after cleanup.

Select a saved profile from devices.codex_profiles.execution. Use profile_id and
optionally presentation with codex_submit. codex_continue uses a new stable
request_id for the same owned thread. Repeat identical requests with their original
IDs; uncertain dispatch must be reconciled, never replaced with a fresh task.

The SSH/experiment helper and local/remote approval forwarding are retired. Do not
call retired endpoints or pass server_profile_id / experiment_id, including empty
values. Legacy records remain queryable and cancellable but cannot be resumed
through retired workflows. Do not delete user SSH files or experiment records.
Normal SSH work may be performed by target Codex when the user requests it, using
its own network and permissions. Secrets stay in the target's normal secure UI.

Unexpected approval or input requirements trigger interruption and target-local
handling. Never approve on the user's behalf or change the permission profile to
bypass a denial. Unconfirmed stop remains uncertain; do not dispatch a replacement.
Report official turn completion and actual command output separately. Terminal
tasks do not require images. The native Computer Use gate remains closed.

After checking the source tool catalog, read `devices.capabilities` before choosing
an execution mode. Basic Cua desktop control, Codex connection,
and official Computer Use acceptance are separate. If native delegation is gated,
say “Codex 桌面委派暂不可用” with the returned reason. Never replace it with Cua,
private helper protocols, model completion text, or a newly attached target.
Native desktop delegation requires actual approval/interruption/image acceptance.
Terminal delegation uses its separate capability; images are not required.
Delegated tasks consume the target Codex account's quota.

## Recovery and report

For reading existing Codex conversations, use the sibling `share-codex-history`
skill and the local **会话共享** view. This has independent opt-in
and does not require terminal execution or desktop permissions. First enable
defaults to **全部会话（默认）**, including future local conversations. An
existing installation reflects its saved mode; enabling again preserves it.
**仅白名单中的会话** is optional and reveals the search/candidate controls.
Only an explicitly saved empty allowlist denies all; not selecting allowlist
mode leaves the all-conversations scope. Mode and candidate changes are drafts:
use **预览共享范围** then **确认并保存共享范围** for either mode. The
**当前共享范围** status shows the saved scope, and polling must preserve drafts.
Refresh the existing official plugin and check for 13 actual tools,
including `codex_projects`, `codex_threads`, `codex_read_thread`. Do not run a
terminal task to bypass a history refusal. Group changes do not grant access.

Use the wizard's redacted diagnostic export. Distinguish missing key, paused,
locked, unavailable activity, driver error, DNS, TLS and tunnel client exit.
Also distinguish proxy limitations/authentication, stale cloud evidence and
settings pending the next tunnel start. The network settings remain local-only; MCP still has
13 tools and does not expose a remote network configuration action.
No secret values, typed content or full desktop content belong in diagnostics.
Report verified stages, exact remaining blocker, and required local interaction.
Do not call an incomplete setup “ready”.

## Console navigation

The local page has four views: 概览, 连接设置, Codex, 会话共享. Installed devices
start on the overview; missing configuration offers 继续设置 without granting access.
Account/tunnel/key/proxy and redacted diagnostics are in 连接设置. Project permissions
are collapsed in Codex. History mode/selection/preview/apply are in 会话共享;
**查看技能待确认清单** is available in both sharing modes. Status polling must
not erase unsaved mode, form edits or candidate selections. Editing a draft or
discovering a newer saved revision invalidates its preview; preview again. Reopen
the page once after an agent restart; ordinary refresh and multiple tabs reuse the
authenticated browser session. Never share a launch credential with source Dot.
