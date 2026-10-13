# Local connections

This package owns generic local connection references. It does not own Codex
authentication, permission-policy definitions, desktop execution, or deployment.
No example or test contains a real server endpoint or credential.

## Storage API

`NewStore(absoluteDedicatedStateDir)` returns a store for `connections.json` and
`connections.lock`. Choose a dedicated directory under DotControl's local state
directory; its parents must be trusted. Reads do not create a directory.

- `Load() (Config, error)` returns schema version 1 and empty profiles for a new
  store. Unknown JSON fields, unsupported versions, duplicate IDs, invalid
  references, links and malformed state are rejected, never overwritten.
- `UpsertExecution(ExecutionProfile)` returns the saved profile. Empty IDs receive random stable IDs; pass that ID on edits.
  Unrelated records and the Codex executable reference remain intact.
- `SetCodexExecutable(path)` changes only that reference; an empty value clears
  the override and lets the caller use discovery.
- `Update(func(*Config) error)` is the locked read-modify-write primitive. The
  callback must not reenter the store. A concurrent writer gets
  `connections_locked`; callers may retry after refreshing state. Callback errors
  are reduced to `update_rejected` so diagnostics cannot leak local input.

Writes use a private, uniquely named temporary file, flush it, and replace the
destination in the same directory. Windows applies a protected current-user DACL
to the directory, lock and data files, and uses replacement with write-through.
Unix uses directory mode 0700, file mode 0600, atomic rename and a directory flush.
Errors after replacement may indicate a durability failure even if the new
document is already visible; callers should reload before retrying.

`Config` stores `version`, optional `codex_executable`, `execution_profiles`, and
`server_profiles`. Execution profiles store `id`, `name`, absolute `project_dir`,
`allowed_apps`, `presentation` (default `visible`, or `background`),
`permission_profile` (a reference, not permission definitions), and `execution`
(`terminal` or `computer_use`). Validation does not establish that a project
directory exists or that a permission reference is available; integration must
resolve those before execution.

Server profiles store `id`, `name`, `ssh_alias`, optional `ssh_port` (1–65535;
omission uses SSH configuration), optional absolute POSIX `remote_dir`, optional
`deploy_command`, and optional absolute local `identity_file`. The destination
may be an SSH config alias, a hostname/IP, or `user@host`; IPv6 is accepted.
Options, control characters and shell syntax are rejected. Store key **paths**
only. Passwords, private-key contents, API tokens and other credentials have no
schema fields; never put credentials in names, commands or other text fields.
Deployment commands are inert saved text and are never run by this package.

`Config` and profile values contain private local references. They belong only
in authenticated local configuration views, never remote status output or logs.
All errors are fixed codes. `Status` is the safe public result type.

## Codex discovery

`DiscoverCodex(ctx, configuredPath, DiscoveryOptions{})` returns a
`DiscoveryResult`. Its `Executable` is local-only and excluded from JSON; safe
JSON fields are `available`, `code`, `source` and sanitized `version`.

An explicit configured path is authoritative and fails closed. Without one,
the order is:

1. A Codex executable in a recognized desktop resource/runtime path whose
   parent chain reaches the official desktop application.
2. Installed desktop bundle/runtime candidates.
3. The executable resolved as `codex` on PATH.

Windows enumerates public executable paths and parent process IDs through the
OS. macOS reads the equivalent `ps` fields. Discovery does not parse Codex
authentication/configuration databases, command lines, environments, or chats.
Process metadata has a separate 1 MiB capture limit. Truncated scans are discarded
in full, never parsed as partial ancestry evidence; installed/PATH discovery can
still proceed without running-process evidence.
Version checks invoke the candidate with `--version`, a three-second per-probe
deadline and a ten-second overall deadline. Source classification is path and
ancestry evidence, **not** a code-signature verification or proof of computer-use
capability. `DiscoveryOptions.Candidates` and `.Version` are injection seams.
Discovery does not automatically save the selected executable.

## Retired server operations

Server profiles remain decodable and are preserved when editing ordinary Codex
configuration. `UpsertServer` is a compatibility tombstone returning
`feature_retired`; the product no longer edits server profiles, authorizes public
keys, performs SSH preflight, or executes remote scripts. These operations do not
read or modify the user's SSH configuration, identity files or remote machines.
