# Personal Dot automatic routing

Approved goal: personal Dot is an automatic target for both text answers in Magpie routing groups and durable background cloud tasks. The user currently connects Dot through ChatGPT only. Existing manual context handoff remains useful input, not the finished goal.

## Supported transport to verify

OpenAI's MCP Events guide explicitly supports dots using protocol 2026-07-28. The user connects the private MCP plugin and asks Dot to subscribe to a task queue. DotControl stores submissions, emits a signed event to ChatGPT's supplied HTTPS callback, and provides authenticated tools to claim/read/update/complete tasks. ChatGPT's inbound MCP requests use the private tunnel. No new public listener or Slack/Teams account is required. Event receipt is not proof of execution. Live subscription/tool roundtrip must pass before readiness is advertised.

Source: https://developers.openai.com/plugins/build/mcp-events and https://developers.openai.com/plugins/deploy/connect-chatgpt . Follow OpenAI's current supported webhook subset, not all experimental event delivery modes.

## Modes

Text routing initially accepts only supported text requests. Preserve roles and session references; reject or skip tools/images/strict schema before enqueue, without discarding fields. Tool-using Codex/OpenCode compatibility requires a separate verified agent protocol capability.

Background tasks return persistent job IDs, progress, results and cancellation intent. They target Dot's own execution; existing DotControl target-Codex delegation must never be relabeled personal Dot cloud execution.

## Core invariants

- Explicit local opt-in; a separately paired Magpie client credential grants only its task queue submit/read/cancel operations, never existing admin/MCP credentials or desktop/history access.
- Durable request ID plus content digest makes retries idempotent. A conflicting reuse fails. Event retry preserves event ID and never creates a replacement job.
- Atomic claim binds a token to one job; duplicate events do not re-execute. Lost claims/leases become outcome_unknown rather than automatically executing a second cloud task.
- Persist queue/outbox before notification; persist accepted results. Corrupt/unreadable state fails closed. Shared state changes across processes serialize and replace atomically.
- Pause/revoke/disable block reads, claims and writes; in-flight operations recheck authority before publishing. Secret material uses the OS vault or encryption keys there.
- Subscription identity binds authenticated principal, canonical filters, event and callback. Persist expiration and signing secrets for granted lifetime.
- Callback delivery validates public HTTPS destinations at connection time, blocks local/private ranges and redirects, verifies challenges, and uses Standard Webhooks HMAC. Bounded delivery deadlines/retries; 410/413 stop retry. Send job IDs and small metadata rather than full prompts.
- A verified local queue acceptance must propagate through trusted plugin-host IPC/core state, not an untrusted upstream header. After acceptance, no gateway candidate/account/mirror fallback may duplicate the task. Client timeouts preserve original job references; cancellation of HTTP waiting does not prove cloud cancellation.
- Magpie's holdWriter can send HTTP 200/SSE comments while still falling back. Those are not a commit barrier. Never manufacture model answer text to lock routing.
- Text wait default is 120 seconds, with SSE comments only as protocol keepalive; no fabricated token streaming/usage. MCP tools are short calls within the tunnel's 120-second budget; cloud work runs outside them.

## Incremental delivery

1. DotControl durable queue, scoped client API, event discovery/subscription/delivery, claim/progress/complete tools and onboarding/probe commands integrated into serve.
2. Real ChatGPT/Dot subscription plus nonce-response roundtrip, duplicate/timeout evidence and observed readiness.
3. Magpie text provider/routing integration with capability preflight and acceptance barrier; isolated fixtures before live use.
4. Durable background task entry and status/results, using selected context.
5. Verified tool-call protocol before advertising agent-model compatibility.

Each slice must be usable/testable, with independent review, deterministic fixtures, and no real-account evidence invented from tests. Upstream Magpie declines external PRs, so deliver fork drafts; DotControl changes can be separate small PRs. No merge/release merely from fixture passing.
