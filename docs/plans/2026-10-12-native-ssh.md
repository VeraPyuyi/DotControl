# Native Computer Use and SSH experiment closure

Implement the accepted plan incrementally on PR #1. Source Dot stays attached to
Mac; Windows is the execution target. Automated tests, target-only probes, and
real source-to-target acceptance are separate evidence categories.

- [x] Cancellable idle wait, deferred official-thread presentation, trusted local
  terminal review and confirmed-stop native handoff.
- [x] Local public-key authorization through interactive system OpenSSH; no
  password field, credential body, automatic host trust, or sshd changes.
- [x] Bundled idempotent experiment submit/status/explicit-stop helper, configured
  server references, independent SSH result verification.
- [x] MCP experiment reference and result metadata, local approval/authorization UI.
- [x] Regression, race, security and compatibility review; three-platform builds.
- [ ] Real official calculator action/image, refusal/cancel/human takeover.
- [ ] Real authenticated one-core CPU fixture under an independent directory.
  The original /tmp parent failed its permission check; the user authorized a
  new private directory under the login account's home. No permissions were relaxed.
- [ ] Mac source approval/continue/native image rendering and experiment evidence.
- [x] Update draft PR and sanitized support matrix.
- [ ] Enable/release only accepted capabilities. Do not rewrite historical
  unknown/blocked records.

The fixture sums integers 1 through 1,000,000 (expected 500000500000), installs no
server dependencies and uses no GPU. Control cancellation never implies remote
experiment termination. Uncertain dispatches are queried, not replayed.
