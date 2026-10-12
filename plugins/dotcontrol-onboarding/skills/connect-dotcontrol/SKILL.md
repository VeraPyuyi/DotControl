---
name: connect-dotcontrol
description: Use when the user asks to install, connect, repair, or diagnose DotControl on this Windows or Mac, or enable Dot cross-computer control.
---

# Install and connect DotControl

Use the same released installer as the graphical setup. Do not implement an
alternate installer, compile components, install Python/Homebrew/Xcode/Go,
edit credential stores directly, or alter DNS/proxy/TLS settings.

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
   release `https://github.com/VeraPyuyi/DotControl/releases/tag/v0.2.0-alpha.2`.
   Asset names: `DotControl-0.2.0-alpha.2-windows-amd64-setup.exe`,
   `DotControl-0.2.0-alpha.2-macos-arm64.dmg`, or
   `DotControl-0.2.0-alpha.2-macos-amd64.dmg`.
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
activity detection available; tunnel ready; source Dot plugin devices call;
MCP image returned; image actually visible at source Dot. Never substitute
process existence for readiness or local image viewing for cloud delivery.

The device starts paused. Only the local user may enable it in the wizard.
Repair, restart and upgrade preserve the existing pause/revoke state. On Mac,
open system settings for CuaDriver Accessibility/Screen Recording and
DotControlActivity Input Monitoring, then use Repair to recheck. Unknown
permissions are not granted permissions.

For an authorized test, keep source Dot attached to its original computer and
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

Read `devices.capabilities` first. Basic Cua desktop control, Codex connection,
and official Computer Use acceptance are separate. If native delegation is gated,
say “Codex 桌面委派暂不可用” with the returned reason. Never replace it with Cua,
private helper protocols, model completion text, or a newly attached target.
Only delegate when the installed platform has actual approval/interruption/image
acceptance. Delegated tasks consume the target Codex account's quota.

## Recovery and report

Use the wizard's redacted diagnostic export. Distinguish missing key, paused,
locked, unavailable activity, driver error, DNS, TLS and tunnel client exit.
No secret values, typed content or full desktop content belong in diagnostics.
Report verified stages, exact remaining blocker, and required local interaction.
Do not call an incomplete setup “ready”.
