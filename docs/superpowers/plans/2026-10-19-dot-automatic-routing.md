# Dot Automatic Routing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development to implement task-by-task with independent review. User approved execution; proceed without another plan-approval pause.

**Goal:** Connect personal Dot in ChatGPT to Magpie answer routing and durable background tasks using authenticated MCP Events and result tools.

**Architecture:** DotControl owns durable encrypted queue, per-client access and event subscriptions. Magpie calls a loopback bridge, filters unsupported requests before enqueue and stops fallback after a verified queue acceptance. Event delivery and every MCP call are short operations; Dot runs work independently and writes results back.

**Tech Stack:** Existing Go modules, crypto/net/http/json standard library, existing flock/vault. No browser-cookie scraping or normal inference API substitution.

**Spec:** ../specs/2026-10-19-dot-routing-design.md; shared research contract 2026-10-19-dot-routing-wire-contract.md (resolve exact names across both detailed plans before writing product code).

## Global Constraints

- Explicit opt-in, dedicated paired-client credential, private loopback Host/Origin checks.
- No task accepted unless durable queue write succeeds; same request ID and digest returns same job, changed content conflicts.
- Unknown cloud effects never auto-reclaimed/reexecuted; cancel intent does not mean stopped.
- Protocol 2026-07-28 MCP Events webhook only; official discovery events capability must actually be present.
- Event callback HTTPS public addresses revalidated at dial, no redirects; challenge and Standard Webhooks signatures; secret24–64bytes in vault/encrypted state.
- Accept state conveyed by trusted internal control path, no vendor header spoof. Unsupported request must have zero queue writes.
- 120-second text wait default; no fake content/token usage; real Dot roundtrip needed for ready.
- Fixture tests use isolated dirs/vaults, no real configs/chats/secrets/services.

## Review Focus

- Two independent processes enqueueing same request: one persistent job and outbox ID.
- Worker restarts after claim with uncertain effect: outcome_unknown, no automatic second claim.
- DNS change/redirect/private callback: blocked before any secret/data leaves.
- Plugin or local request cancelled after acceptance: routing cannot create another task at another candidate.
- Expired/revoked subscription with queued work: not ready, no data/tool access or silent replay.

## Tasks and dependency graph

1. Durable queue and policy primitives, exact contracts in DotControl detail plan. Test transaction/restart/encryption/malformed state and claim lifecycle first, implement minimum, run focused+race, commit and review.
2. Authenticated loopback client API plus events/MCP tools with per-operation gate. Test unavailable/disabled separation, server discovery exact wire, unsigned/private callback rejection and full fixture roundtrip; implement and review.
3. Runtime/CLI enable/pair/status/probe integration and event worker lifecycle. Default 13 tools unchanged; enabling bridge additive. Test actual serve handler stack with fake vault and callback, shutdown cleanup and pause/revoke; review and full DotControl suite.
4. Magpie capability preflight and accepted-job barrier. Trace actual provider/gateway/plugin contracts before exact APIs; tests must fail for every retry/candidate/account/mirror bypass, upstream headers cannot set acceptance; review.
5. Dot provider local bridge and background task entry/status. Use shared v1 contract, short HTTP operations, stable request IDs and results. Test full gateway stack plus fakebridge/Dot worker (not fake feature modules), session separation and protocol conversion. Review, compile/vet/scope appropriate full suites, create drafts.
6. Real Dot acceptance: setup instructions and randomized probe ready before any account action; verify discovery/subscription/challenge/delivery/claim/completion, no fakefixture-ready setter. If account step is blocked, retain runnable draft and state exact missing evidence.

The two detailed plans name exact files/interfaces/tests. This umbrella plan prevents a queue-only deliverable being called finished routing.
