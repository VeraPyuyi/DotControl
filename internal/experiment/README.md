# Retired built-in experiments

The built-in SSH and bounded CPU experiment implementation was retired in
0.2.0-alpha.5. The legacy `experiment` command returns `feature_retired` without
reading state or invoking SSH. The local `ssh-authorize` command is also retired.

Existing experiment journals, server profiles, SSH configuration, keys and other
user files are retained. Owned historical jobs remain queryable and cancellation
records intent; it does not stop a remote experiment. Old request IDs return their
authenticated recorded result without dispatch. New or continued built-in
experiment requests are rejected.

Ordinary terminal tasks continue through official Codex using locally selected
permissions. DotControl does not inject an experiment helper or perform SSH
status readback.
