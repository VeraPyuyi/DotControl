# Dot bridge

The Dot bridge is opt-in via `dot_bridge_enabled` in local config. It does not change the default 13-tool MCP catalog unless the runtime is started with bridge support enabled.

## Operator flow

1. Enable bridge support in config and start `dotcontrol serve`.
2. Run `dotcontrol bridge enable`.
3. Run `dotcontrol bridge pair` and import the secure pairing file into Magpie.
4. Connect the private Dot plugin, refresh MCP metadata, and subscribe to `dot.task.available`.
5. Run `dotcontrol bridge probe <client_id>` and complete the probe job through MCP claim/complete.
6. Poll `GET /dot-bridge/v1/status` with the paired Bearer token.

## Security notes

- Loopback client API uses Bearer tokens only; queue scope is resolved from the paired token.
- MCP task tools bind to the configured bridge queue, not arbitrary client input.
- Readiness is derived from a real probe claim/completion lifecycle; there is no manual readiness setter.
- The bridge cannot cryptographically prove that Dot itself completed the probe versus another authorized MCP caller using the same tunnel credential.
