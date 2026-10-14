# Sidebar Organizer attribution

`internal/historyaccess/sidebar.go` adapts the pure metadata identity,
availability, membership indexing, and deterministic fingerprint approach from
Sidebar Organizer's `sidebar_plan.py`.

- Upstream: https://github.com/VeraPyuyi/codex-sidebar-organizer
- Source revision: `6b51e47544d29518f03017d1dea98cb498b2796c`
- Source path: `plugins/sidebar-organizer/skills/organize-sidebar/scripts/sidebar_plan.py`
- Inspected source file SHA-256: `2f4dadd0abde8cdca46eb1d3abc40d232d58f878fda4e11c5872899398624239`
- Copyright: Copyright (c) 2026 VeraPyuyi
- License: MIT; full text in `licenses/sidebar-organizer-MIT-LICENSE`.

This is a Go adaptation with a narrower purpose. It accepts caller-supplied
native `list_threads` and `list_projects` metadata, selects only exact stable
Codex thread IDs on the explicit `local` host, and reports incomplete or
ambiguous references. It never changes sidebar organization. Candidate output
does not grant history access.

The original `capture_identity`, private application-state reader,
`reconcile`, cloud evidence readers, mutation planning, and CLI were not ported.
No private Codex state file, history body, database, HTTP API, or filesystem
discovery is consulted by the resolver. Names and project paths are not identity
evidence. Sections are indexed by their full native section IDs, never merged
by their display names. The snapshot hash uses canonical Go JSON with all native
fields preserved; it is not a byte-for-byte reproduction of Python's JSON hash.
