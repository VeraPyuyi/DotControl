# Native Computer Use and SSH experiment closure

Implement the accepted plan incrementally on PR #1. Source Dot stays attached to
Mac; Windows is the execution target. Automated tests, target-only probes, and
real source-to-target acceptance are separate evidence categories.

- [ ] Cancellable idle wait, deferred official-thread presentation, trusted local
  terminal review and confirmed-stop native handoff.
- [ ] Local public-key authorization through interactive system OpenSSH; no
  password field, credential body, automatic host trust, or sshd changes.
- [ ] Bundled idempotent experiment submit/status/explicit-stop helper, configured
  server references, independent SSH result verification.
- [ ] MCP experiment reference and result metadata, local approval/authorization UI.
- [ ] Regression, race, security and compatibility review; three-platform builds.
- [ ] Real official calculator action/image, refusal/cancel/human takeover.
- [ ] Real authenticated one-core CPU fixture under an independent /tmp directory.
- [ ] Mac source approval/continue/native image rendering and experiment evidence.
- [ ] Update draft PR and sanitized support matrix; enable/release only accepted
  capabilities. Do not rewrite historical unknown/blocked records.

The fixture sums integers 1 through 1,000,000 (expected 500000500000), installs no
server dependencies and uses no GPU. Control cancellation never implies remote
experiment termination. Uncertain dispatches are queried, not replayed.
