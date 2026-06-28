# m-iris docs

Documentation for **m-iris** — the InterSystems IRIS engine driver (D1) in the
`m-driver-sdk` ⟷ `m-iris` ⟷ `m-ydb` driver-coordination effort. Repo rules:
`../CLAUDE.md` (per-repo) + `~/vista-cloud-dev/CLAUDE.md` (org).

## Live trackers (Tier-D — kept at `docs/` root by driver carve-out)
- [`m-iris-tracker.md`](m-iris-tracker.md) — the driver effort's live implementation
  tracker (the step-2 target for this repo's increments; the coordinator rolls the
  shared `driver-implementation-plan.md` up at milestone boundaries).
- [`m-iris-driver-status.md`](m-iris-driver-status.md) — driver status / progress
  narrative across the M0–M4 milestones.

These two stay at root for the life of the driver effort (spans future milestones) —
do not move or archive them.

## Folders
- [`guides/`](guides/) — how-to guides.
  - [`claude-code-permissions-guide.md`](guides/claude-code-permissions-guide.md) —
    machine-agnostic blueprint for Claude Code permission configuration.
- [`memory/`](memory/) — auto-memory for this repo (driver-local; recalled by an
  m-iris session). Durable IRIS/driver canon only — see [`memory/MEMORY.md`](memory/MEMORY.md)
  for the index.

Cross-repo coordination docs (the contract, the shared plan, coordination model) live
in the central `docs` repo, not here.
