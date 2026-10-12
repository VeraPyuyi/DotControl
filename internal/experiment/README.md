# Bounded CPU experiment helper

The installed executable accepts these commands without a running device agent:

```text
dotcontrol --state <private-state> experiment status --profile <saved-server-id> --experiment-id <stable-id>
dotcontrol --state <private-state> experiment submit --profile <saved-server-id> --experiment-id <stable-id>
dotcontrol --state <private-state> experiment stop --profile <saved-server-id> --experiment-id <stable-id>
```

Only `sum-1-to-1000000-v1` is supported. It runs one AWK process, requires no
installation or GPU, and expects the exact bytes `500000500000\n`. No configured
deployment command is executed. Local connection references remain private.

Before dispatching an experiment task, the bridge saves a separate immutable
`<id>.binding.json` from the accepted profile and fixture snapshot, and records
its digest in the authenticated runtime journal. All helper operations check
this local binding even if `--binding` is omitted. Codex commands also pass the
expected digest explicitly, and independent readback requires the journal's
digest. Missing bindings or mid-turn profile edits fail before SSH or intent
creation. Preparing a binding never consumes or resets a launch intent.

Before the first remote submission, a private immutable intent binds the ID to
the full saved profile snapshot and worker digest. An existing intent can only
query remote state, including after a helper restart or a lost response. A
missing remote record after intent remains `outcome_unknown`; it is never
automatically resubmitted. Profile or fixture changes conflict.

The remote directory is `<remote_dir>/dotcontrol-<stable-id>`. Its atomic creation
claims the launch. Private ownership, permission, symlink, and hardlink checks
protect evidence reads and writes. Worker identity includes PID, Linux process
start time, boot ID, and working directory. Completion requires a separate SSH
status command checking the actual result bytes, hash, expected check and exit
code. A transport exit code or model message alone cannot establish completion.

Stop is a separate explicit operation. After checking the recorded process
identity, it writes an owned cooperative request. The single AWK process checks
that request. No PID or process group is signalled, avoiding PID-reuse races.
`stop_requested` is not proof of stopped execution; query until `stopped` or a
terminal result. Cancelling the Codex controller does not call this operation.

`codex_submit.experiment_id` is optional and requires a saved server profile.
Official `exec_command` receives helper argument arrays containing only state,
profile and ID references. `codex_job` independently invokes helper status and
returns `runtime.experiment` plus an `independent_ssh_helper_readback` evidence
item. The Codex turn state remains separate. Readback never rewrites historic
blocked/unknown receipts or their authenticated journal hashes.

Tests use only fictional profile destinations. Windows tests cover protocol and
durable intent behavior plus GUI-subsystem redirected JSON; Linux tests execute
the shell fixture locally with real `/proc` and filesystem checks. The Linux CI
job does not establish acceptance on any user server or enable platform gates.
