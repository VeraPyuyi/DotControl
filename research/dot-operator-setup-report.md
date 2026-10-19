# Dot operator setup report

## Delivered in this worktree (operator-owned files)

- `dotcontrol bridge enable` bootstraps offline: when `dot_bridge_enabled` is false, the CLI reloads installed `config.json`, sets only that flag via `config.Save`, prints `restart_required`, and does not call admin or start a duplicate runtime.
- `dotcontrol bridge pair` redacts admin responses to `pairing_file`, `client_id`, and `queue_id` only.
- `dotcontrol bridge probe <client_id> [--mode answer|task]` sends `mode` in admin JSON (default `answer`).
- `dotcontrol bridge jobs <client_id>` calls `GET /dot-bridge/v1/jobs` (not `/status`).
- Bridge client HTTP: numeric loopback validation before credential load, 10s timeout, `Proxy: nil`, no redirects.
- Pairing and bridge state private file helpers in `internal/dotbridge/pairing*.go`: current-user ACL (Windows protected DACL), `0600`/`0700` Unix, ancestor trusted-parent checks, symlink/reparse/hardlink rejection, atomic replace, `client_id` validation.

## Controller integration still required (not edited here)

| Area | Caller | Action |
|------|--------|--------|
| Bridge state persistence | `service.loadState` / `service.saveState` | Replace raw `os.ReadFile`/`os.WriteFile` on `bridge.state.json` with `securelyReadBridgeState` / `securelyWriteBridgeState` from `internal/dotbridge/pairing.go`. Preserve `os.IsNotExist` → empty state on first load. |
| Admin pair response | `runtime.go` `/admin/bridge/pair` | Ensure JSON never includes `token` (today returns only path + IDs; `Service.Pair` still returns token to handler—do not echo it). |
| Admin probe mode | `runtime.go` `/admin/bridge/probe` | Decode `mode` from body; call `dotBridge.StartProbeMode(clientID, mode)` instead of `StartProbe` only. |
| Job list HTTP | `api.go` `handleJobs` | Route `GET /dot-bridge/v1/jobs` to scoped list (metadata-only `JobDTO`, `result` omitted); keep `POST` for submit. Depends on `QueueAPI.List` / `dotqueue` list export (commit `41f2748` area). |
| Windows DACL tests | `pairing_operator_test.go` or platform test | Add junction/reparse + protected DACL assertions mirroring `dotqueue/files_windows_test.go` once helpers are wired in CI. |

## Pair API token risk

`Service.Pair` returns `(PairingFile, path, err)` including the plaintext token for writing the local file. That is correct for in-process pairing. Risk is only if any admin or CLI layer serializes the full `PairingFile` to stdout or HTTP; operator CLI now filters pair output.

## Tests run

```text
go test ./cmd/dotcontrol -run TestBridgeOperator -count=1
```

`internal/dotbridge` operator pairing tests require a clean build of sibling `probe.go` changes from the core worker (unused variable in flight at time of last run).
