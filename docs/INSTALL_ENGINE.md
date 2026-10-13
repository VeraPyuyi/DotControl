# Transactional install engine

`internal/install` installs already built local bundles. It does not build source,
install system dependencies, migrate configuration, access the credential vault,
or install the macOS driver into `/Applications`. The launcher owns platform
service lifecycle and any separately authorized driver installation.

## Bundle contract

The bundle directory contains `manifest.json` and exactly the regular files named
by its manifest, with only their required parent directories:

```json
{
  "version": "0.2.0",
  "os": "windows",
  "arch": "amd64",
  "files": [
    {"path": "dotcontrol.exe", "sha256": "<64 lowercase hexadecimal digits>", "mode": 493}
  ]
}
```

`mode` is an integer Unix permission mask (`0755` = `493`, `0644` = `420`). File
bytes are copied without transformation; Unix permission bits are verified and
preserved. Windows does not have an executable mode bit, so its source mode is
not compared against the Unix mask. Directories are created with mode `0755`.
Target OS and architecture must match the running installer. Payload signatures
are not rewritten or re-signed by this package; signature/notarization checks
belong to the platform launcher. A payload containing framework symlinks cannot
be flattened into this regular-file manifest: keep a signed application in a
verified archive for its separate platform installation.

Paths must use `/`, remain relative, and contain no dot segments, backslashes,
alternate data streams, Windows reserved names, or trailing spaces/dots.
Case-insensitive duplicates and file/directory conflicts are rejected on all
platforms. Verification rejects extra paths, missing files, links, Windows
reparse points, hardlinks, and malformed hashes. Existing ancestor aliases are
also rejected, rather than silently resolved to a different install or state
location.

## API

```go
manifest, err := install.Verify(ctx, bundleDir)
result, err := install.Apply(ctx, install.Options{
    Root: root, StateDir: stateDir, BundleDir: bundleDir,
    BeforeActivate: func(ctx context.Context, target install.Current) error { /* stop old service */ return nil },
    Health: func(ctx context.Context, target install.Current) error { /* start target and probe */ return nil },
    CleanupOldVersions: true,
    BeforeCleanup: func(ctx context.Context, old install.Current) error { /* reject running or referenced old generation */ return nil },
    Rollback: func(ctx context.Context, previous *install.Current) error { /* stop failed target; restart previous */ return nil },
    Progress: func(p install.Progress) { /* display persisted phase */ },
})
result, err = install.Repair(ctx, options)
current, err := install.ReadCurrent(root)
progress, err := install.ReadProgress(root)
err = install.Remove(ctx, install.RemoveOptions{
    Root: root, StateDir: stateDir,
    BeforeRemove: func(ctx context.Context) error { /* stop and unregister owned service */ return nil },
})
```

Both root and state directory are mandatory. The root cannot be a filesystem
root, equal the state directory, or contain it. State may be the root's parent,
as on macOS. `DefaultRoot()` returns `%LOCALAPPDATA%/DotControl` on Windows and
`~/Library/Application Support/DotControl/program` on macOS. The existing
configuration directory and vault service identity remain the caller's choices.

`Current` contains `Version`, `OS`, `Arch`, `ManifestSHA256` and the absolute `Dir`
of the installed version. `Result` also exposes `Previous`, `Reused`, and
`CleanupPending`. If cleanup fails after health commits, `Current` remains the
new active generation, `CleanupPending` is true, and
`errors.Is(err, install.ErrCleanupPending)` is true. The error contains the
specific cleanup failure. Callers must keep that runtime active and present the
pending cleanup; they must not treat this as an activation failure or roll back.
Manifest identity is SHA-256 of canonical JSON after sorting files by path;
whitespace and file-list ordering do not create a new release identity. The same
version with a different canonical manifest is rejected.

## Transaction and recovery

An OS-backed per-root lock serializes installers across processes, including
callbacks. Lock acquisition honors cancellation. Root metadata is updated with
same-directory temporary writes, file synchronization and rename. Phases are
persisted in `transaction.json`: `download`, `verify`, `stage`, `install`,
`waiting_for_exit`, `health`, `cleaning_old_versions`, `cleanup_pending`,
`complete`, or `failed`. The local-bundle `download` phase means acquisition is
already satisfied; no network request is made by `Apply`.

Verified files are copied into `.staging/<manifest-hash>`. Retry re-verifies every
existing file; a partial or corrupt listed regular file is replaced from the
verified source. Unknown staging files and links fail closed. Completed staging
is renamed into `versions/<version>-<hash-prefix>`, which is not modified during
normal upgrade. `Repair` uses a new generation directory if the installed copy
is damaged. By default, old versions are retained. Setting `CleanupOldVersions`
opts into removal after successful activation and health confirmation.

`BeforeActivate` runs before the pointer changes. The journal records rollback
intent before invoking this callback and before atomically switching
`current.json`; `Health` runs with the target already active. Callback failure
or cancellation restores the previous pointer,
then calls `Rollback` using a context without the failed operation's cancellation.
For first installation, rollback removes the pointer and passes nil. Callbacks
must enforce their own service timeouts. A failure inside `BeforeActivate` also
invokes `Rollback`, since that callback may have partly stopped the old runtime.

With cleanup enabled, the sequence is verified staging, `waiting_for_exit` and
`BeforeActivate`, activation and `Health`, then old payload cleanup. The previous
payload remains intact until health passes. The `cleaning_old_versions` journal
entry atomically clears rollback intent before any old file can be deleted.
After that commit, cancellation, cleanup errors, and interruption retain the new
pointer. Cleanup failures persist `cleanup_pending`; successful cleanup ends in
`complete`. The engine never stops processes itself.

`BeforeCleanup` runs for each owned generation except the current target, before
any of that generation's files are removed. It is required when there are old
generations to clean. The launcher must reject any running or referenced
generation, including one containing its own installer/launcher executable, and
prevent new references during cleanup. The callback must be idempotent and
tolerate already missing files on retry. A refusal leaves cleanup pending.

The next `Apply` or `Repair` resumes committed cleanup before verifying its
requested bundle. `BundleDir` may be empty for a cleanup-only retry. A nonempty
bundle proceeds through normal installation after cleanup succeeds. A missing
guard fails closed even on retry. `Download` preserves the cleanup journal while
acquiring bytes, so acquisition cannot erase recovery intent. Recovery from a
committed cleanup never invokes `Rollback` with a partially deleted generation.

On the next locked operation, an interrupted activation is rolled back before
another install proceeds. Journal recovery restores the executable pointer and
invokes the supplied `Rollback` callback, even if the requested new bundle is
unavailable. If runtime rollback fails, recovery remains pending and the next
operation retries it. Callers using services must supply an idempotent rollback
callback to both `Apply`/`Repair` and `Download`. Reads of current metadata do not
run recovery or independently verify executable health.

Process interruption is covered by tests. Sudden power-loss durability depends
on the host filesystem's rename guarantees; parent directories are not fsynced.
The engine assumes the current user's private install root is not being modified
concurrently by an adversary. It validates links before access and rechecks open
file identities, but is not a privileged installer or a defense against malicious
same-user directory substitution races.

## Removal and preserved data

`.dotcontrol-install.json` records the owned manifest inventory for each installed
generation. Removal only deletes these recorded files, their manifests, and
empty parent directories. Extra user files are preserved. State and credential
storage are never enumerated or deleted. The service hook must stop owned
executables before file deletion; locked executable errors are returned.

The lock and an empty ownership marker remain after successful uninstall. This
allows repeated uninstall while preserving unknown files. Download caches and
unfinished staging directories also remain, rather than recursively deleting
unverified content. Repair can reinstall alongside an old directory retained
because it contains user files.

Upgrade cleanup uses the existing schema-1 ownership record without changing its
format. A new cleanup-enabled activation requires an explicit successful Health
callback; an already committed cleanup-only retry does not run Health again.
Before removing an old listed file, it verifies its regular-file type,
hash, Unix mode where applicable, and absence of links/reparse points/hardlinks.
A changed or unsafe listed path is preserved and produces `cleanup_pending`;
other files may already have been removed. The user can restore its expected
bytes or move the changed file to an unowned path before retrying. Changed
manifest content is likewise preserved. Missing listed files are treated as
already removed. Ownership is retired only after all listed payload files and
the matching manifest are absent. Only their empty parent directories are
removed; unknown files and directories can keep the old directory present after
successful cleanup. No state or vault directory is enumerated. Explicit
uninstall retains its existing ownership-based removal behavior.

## Optional pinned HTTPS acquisition

`Download(ctx, DownloadOptions{Root, StateDir, URL, SHA256})` returns a verified
archive under `.downloads/<sha256>.bundle`. It supports byte-range continuation
of `.part` files, handles servers that restart with status 200, validates 206
ranges, disallows HTTPS-to-HTTP redirects and embedded URL credentials, and
verifies the final digest before exposing the completed file. The default size
limit is 2 GiB; `MaxBytes` overrides it. `Client` can provide a transport while
HTTPS redirect restrictions remain enforced.

Download does not extract or execute the archive. The launcher must use an
appropriate safe extractor and then pass the extracted bundle to `Verify`/`Apply`.
The trusted launcher supplies the pin; a manifest bundled with its own payload
alone establishes consistency, not publisher authenticity.
