# Permanent MCP Events subscriptions

Following https://developers.openai.com/apps-sdk/build/mcp-events, an explicit JSON `ttlMs: null` grants a subscription without time expiration and returns `refreshBefore: null`. Omitting the field retains the one-hour default. Numeric durations retain the existing accepted range of 60 seconds through 24 hours; out-of-range values are rejected. A numeric refresh of a permanent subscription grants a finite lease.

Encrypted event records and bridge subscription proofs carry an explicit `Permanent` flag with zero expiry. Zero expiry alone never grants permanence: existing invalidation and legacy records remain inactive. Public status reports an active permanent subscription with `renewal_state: current` and omitted `expires_at` and `refresh_before`. The HTTP subscribe result always includes `refreshBefore`, including JSON null.

Permanence changes only time expiration. Delivery still requires enabled state and callback verification. Unsubscribe, revocation, disable/re-enable, authentication continuity changes and signing-secret replacement retain existing generation and trust-epoch invalidation rules. Event and bridge grants are intersected: a permanent event-only renewal cannot extend a finite bridge grant, and a finite event grant bounds a permanent bridge grant.

A subscription does not grant answer/task capabilities by itself. Each mode still requires completion of its nonce proof. Once proven, continuous proof remains valid within the same binding, auth/pairing identity, consumer and trust epoch; permanent leases therefore need no manual ten-minute proof churn. Restart preserves this evidence. Invalidating continuity requires a fresh proof, even if a new permanent subscription uses the same deterministic ID.

Callback challenge caching is separate from continuous mode proof. It remains bounded to five minutes and any finite source/target grant. Permanence does not relax HTTPS/SSRF checks, signed challenges, delivery verification, maxActiveSubs, delivery-record limits or encrypted-state size bounds. Existing finite records with null request parameters are not retrospectively upgraded on restart; a legacy bridge proof without a trust epoch remains within the existing epoch check.

Magpie supports nullable permanent subscription dates at ad28f447 (PR #3), so its status card handles omitted `expires_at` and `refresh_before` values without zero dates.

Persisted grants fail closed on load. A permanent event record requires zero expiry and retained `ttlMs: null`; finite event records require a non-zero expiry. A permanent bridge proof requires zero expiry and complete trust evidence. Legacy nullable records without the permanent flag remain finite. Invalid records make the encrypted state load as `corrupt` (events) or `bridge_state_corrupt` (bridge) instead of becoming active.
