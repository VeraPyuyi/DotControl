# Bounded CPU experiment helper

The installed executable accepts these commands without a running device agent:

```text
dotcontrol --state <private-state> experiment status --profile <saved-server-id> --experiment-id <stable-id>
dotcontrol --state <private-state> experiment submit --profile <saved-server-id> --experiment-id <stable-id>
dotcontrol --state <private-state> experiment stop --profile <saved-server-id> --experiment-id <stable-id>
```

On Windows the shipped executable uses the GUI subsystem. PowerShell must
explicitly pipe/capture the invocation to wait for JSON and retain its real exit
code. Codex task instructions provide the exact, quoted command for each
operation, including its binding digest. The supported pattern is:

```powershell
$ErrorActionPreference = 'Continue'
$PSNativeCommandUseErrorActionPreference = $false
# argumentPayload is the Base64 payload from the generated per-operation command.
[string[]]$dcCommand = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($argumentPayload)) | ConvertFrom-Json
$dcExecutable = $dcCommand[0]
$dcArgs = @($dcCommand | Select-Object -Skip 1)
$dcOutput = @(& $dcExecutable @dcArgs 2>&1 | ForEach-Object { $_.ToString() })
$dcExit = $LASTEXITCODE
foreach ($dcLine in $dcOutput) { [Console]::Out.WriteLine($dcLine) }
if ($null -eq $dcExit) { [Console]::Error.WriteLine('experiment_exit_unconfirmed'); exit 1 }
exit $dcExit
```

The generated command encodes the complete UTF-8 JSON argument array as Base64
and decodes it into values before invoking the executable. This also supports
paths containing Unicode smart quotes, which PowerShell otherwise interprets as
quote delimiters. The readable argument arrays remain in the task instructions;
each command also starts with the exact argument array as a safe, ASCII-escaped
JSON comment for human review. The decoded data is never evaluated as shell code. Use the generated command for
actual paths rather than constructing new shell literals from them.

A bare PowerShell call can return early with empty output; that is not evidence
of a successful helper operation. This pattern is tested with both PowerShell 7
and Windows PowerShell against the actual GUI-subsystem binary.

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
