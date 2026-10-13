---
name: connect-dotcontrol
description: Use when the user asks to install, connect, repair, or diagnose DotControl on this Windows or Mac, or enable Dot cross-computer control.
---

# Install and connect DotControl

Use the same released installer as the graphical setup. Do not implement an
alternate installer, compile components, install Python/Homebrew/Xcode/Go,
edit credential stores directly, or alter system DNS/proxy/TLS settings. Use only
the protected local wizard for DotControl's own network mode; it does not change
system settings.

## Discover and install

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
   release `https://github.com/VeraPyuyi/DotControl/releases/tag/v0.2.0-alpha.4`.
   Asset names: `DotControl-0.2.0-alpha.4-windows-amd64-setup.exe`,
   `DotControl-0.2.0-alpha.4-macos-arm64.dmg`, or
   `DotControl-0.2.0-alpha.4-macos-amd64.dmg`.
   Verify SHA-256 using built-in OS facilities before launching. Do not substitute
   another tag or ignore mismatched/missing assets. A checksum protects download
   integrity; these alpha installers are not publisher-signed/notarized.
5. Launch the installer using normal OS tools and follow its local page. On Mac,
   mount the DMG and open `DotControl Setup.app`. The user must handle any OS
   confirmation. Never remove quarantine, disable Gatekeeper, re-sign CuaDriver,
   or override a signature failure. Report the specific gate instead.

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
poll and source invocation outcomes separate. No alpha.4 release, native-platform
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
do not verify the source tool catalog. The presence of `codex_review` in that
catalog does not establish remote approval acceptance.

Only after all four tools are present, call the same plugin's `devices`, verify
the target device ID, inspect `capabilities.codex_terminal` and select a saved
terminal profile from `codex_profiles.execution`. Submit only when the capability
is available and a profile exists. Do not call apps/observe as a prerequisite,
and never put terminal commands into `execute`.
No driver, activity monitoring, idle time or desktop system permissions are needed.
Pause, revocation and locally selected permissions still apply. Unexpected human
requirements mean target-local handling; do not ask for repeated remote approvals.

Use the target's protected local wizard section “连接本机 Codex” to discover and
save the official desktop executable, choose a local project folder, read its
existing permission profiles, and save a named execution profile. Preserve the
exact official permission ID, including a leading colon. Terminal and Computer
Use are separate execution choices; visible/background controls opening the task
UI and does not make Windows native Computer Use a background input facility.

For SSH, let the user save a named alias or user@host, port, remote directory and
optional private-key file reference in the same local wizard. Use its read-only
connection check. Never ask for a password, passphrase or private-key body in chat.
If authentication fails, guide normal target-local SSH setup or ssh-agent unlock;
do not pass a chat secret as a command argument or change host-key checking.
An optional saved deployment command is not authorization to execute it during setup.

Read devices.codex_profiles to select saved profile IDs without reading private
paths or server details. codex_submit uses profile_id and optionally server_profile_id;
codex_continue uses a new stable request_id for the same owned job. A repeated
request keeps its original ID and body. Unknown dispatch must be reconciled by
querying the original job, never by creating a replacement task or training run.

If an approval is pending, codex_review takes only device_id, job_id and approval_id
and opens the host's human confirmation. Never supply an approval decision through
another tool or invent a response if the host cannot display the form. Wait and
explain the specific target-local interaction. Cancelling a control task does not
stop an already submitted server experiment. Treat that as a separate user action.

Report official turn completion, independently checked output, MCP image return
and source display separately. Do not claim model text is verified experiment output.
The current candidate accepts Windows terminal delegation only after local opt-in;
native desktop and remote review remain gated. Configuring a profile cannot
enable an unaccepted platform. The alpha.4 installer retains this configuration UI; local opt-in remains required.

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
skill and the local **共享 Codex 会话（只读）** page. This has independent opt-in
and does not require terminal execution or desktop permissions. First enable
defaults to all local conversations; restrictions require a reviewed exact-ID
allowlist. Refresh the existing official plugin and check for 14 actual tools,
including `codex_projects`, `codex_threads`, `codex_read_thread`. Do not run a
terminal task to bypass a history refusal. Group changes do not grant access.

Use the wizard's redacted diagnostic export. Distinguish missing key, paused,
locked, unavailable activity, driver error, DNS, TLS and tunnel client exit.
Also distinguish proxy limitations/authentication, stale cloud evidence and
settings pending the next tunnel start. The network settings remain local-only; MCP still has
14 tools and does not expose a remote network configuration action.
No secret values, typed content or full desktop content belong in diagnostics.
Report verified stages, exact remaining blocker, and required local interaction.
Do not call an incomplete setup “ready”.
