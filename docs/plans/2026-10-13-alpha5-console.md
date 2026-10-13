# Alpha.5 implementation and acceptance

Approved scope: four-view local console (overview, connection, Codex, history),
retire built-in SSH/experiments and local/remote approval forwarding, preserve
plain Codex delegation and history sharing, release all three installers and
upgrade local Windows only. Keep the existing draft PR unmerged.

1. Backend: reject retired new request fields, preserve old deduplication/records,
   remove dedicated SSH and approval execution, retain safe unexpected-request
   interruption and existing terminal permission bindings. MCP becomes 13 tools.
2. Frontend: responsive four-view console with truthful status, editable forms
   isolated from refresh, scoped feedback, preserved history selections, and no
   old server/review controls or scripts. No runtime framework or remote assets.
3. Review: spec compliance then code quality; regression and native CI, browser
   validation at wide/narrow/360px widths, genuine official Dot read-only task.
4. Release alpha.5 Windows x64/Mac ARM/Intel installers, helper, checksums and
   licenses; public download verification. Preserve previous release assets.
5. Upgrade using the public Windows installer, confirm normal old process exit,
   retain identity/vault/config/history bytes, validate new runtime, then remove
   owned old programs. Refresh existing plugin, verify 13 tools and live results.

Mac runner checks are not user-Mac runtime acceptance. No SSH, experiment,
desktop input or image task is used for this release's cross-computer acceptance.
