# DotControl implementation plan

Approved 2026-10-10. Goal: an official Dot calls a private MCP plugin and operates a named, separately connected Windows or Mac desktop through Secure MCP Tunnel.

Architecture: Go user-session daemon owns one device, persistent action ledger, observation leases and human-priority arbitration. Local MCP stdio bridge and optional mTLS gateway converge on that daemon. Cua Driver is a pinned subprocess; tunnel-client is independently supervised. No model service and no replication of private Dot state.

- [x] Official entry-point and account diagnostics; real Dot nonce, received image and benign Windows fixture action evidence. That historical fixture's image rendering remains unverified; the later cross-device Calculator image was rendered in Dot.
- [x] Durable single-writer action coordinator, cancellation, restart ambiguity and tests.
- [x] Cua Driver adapter and activity monitor implementations; Mac runtime validation remains separate below.
- [x] MCP tools, local authenticated IPC, OS credential storage, CLI setup/status/pause/revoke.
- [x] Supervised official tunnel implementation and private mTLS gateway with pairing/revocation; Windows official tunnel readiness and private plugin discovery passed, real two-device gateway remains unverified.
- [x] Reproducible fixture/demo, install instructions, protocol/architecture/license documentation.
- [x] Windows automated tests and native fixture GUI validation; broader calculator/editor/browser and physical takeover scenarios remain unverified.
- [x] Official Mac calculator action and receipt scenario: the 2026-10-12 official Dot local task used DotControl to calculate 7×8=56 with exact expect fields. Independent readback confirms all five receipts are succeeded/background/verified; the prior round's five unknown receipts and timestamps remain unchanged. Actual screenshot/AX agreement and local image display are user-reported; original image bytes were not independently inspected here.
- [x] Official Mac screenshot delivery for the new Windows-to-Mac scenario: native create_library_file and files/read succeeded in the official Windows task; the parent Dot returned the attachment and this session visually confirmed 6×8 and 48 rendered in Dot. The original Mac task's old images remain undelivered and its legacy prepare_uploads helper remains unsupported in the previously inspected channel.
- [ ] Complete broader Mac acceptance: calculator evidence does not cover other native applications, physical human takeover, platform-matrix scenarios or the real two-device gateway.
- [x] Verify the core cross-device route while official Dot stays attached to Windows: an official Windows local task used the separate Mac plugin for five background Calculator actions, all independently read back as succeeded/verified, and returned 48 plus an actually rendered image to Dot. Mac was not attached and no Mac local task was used. This demonstrates one remote-target scenario, not simultaneous official attachment or a fully validated device fleet. See validation/cross-device-2026-10-12.json.
- [x] Verify the reverse core route while official Dot stays attached to Mac: the official Mac task used the Windows plugin to calculate 4×9=36, with Windows remaining unattached. This session independently read three succeeded/verified receipts and two unchanged unknown receipts, and visually confirmed the final image rendered in official Dot. The final result and image delivery passed; this does not imply all five actions were individually verified. See validation/mac-to-windows-2026-10-12.json.
- [ ] Complete all-five-verified reverse-direction action acceptance; the historical clear-all and multiply unknown receipts must remain unchanged and must not be replayed to obtain a cleaner report.
- [x] Independent specification and code-quality reviews, corrections and fresh final checks.

Success requires actual official Dot invocation. SDK smoke tests do not substitute for this. Missing account authorization or a reachable Mac is recorded as blocked, never passed.

Implementation scope differs from acceptance. Windows local native GUI, authenticated MCP tests and the official Dot fixture path passed; Mac acceptance is incomplete. This is a development preview, not a claim that all five rollout phases are complete. See validation/ACCEPTANCE.md for per-item evidence and actual external blockers.
