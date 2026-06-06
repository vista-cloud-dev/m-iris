# m-iris implementation tracker (D1)

Per-repo tracker — the step-2 target for m-iris driver sessions (org Increment
Protocol). Update the active row here, in this repo, every increment. The shared
`docs/m-engine-drivers/driver-implementation-plan.md` §5 is the coordinator's
cross-repo roll-up, synced at milestone boundaries — do not edit it from a driver
spike. Status: ☐ todo · ◐ in progress · ☑ done.

Pinned: `m-driver-sdk v0.2.0`. Branch: `m-iris-driver`. Transports: local·docker·remote.

| M | Axis | Status | Notes |
|---|---|---|---|
| M0 | scaffold + SDK seam + `meta` | ☑ | honest caps golden; rename irissync→m-iris |
| M1 | lifecycle + health + doctor | ☑ | remote/attach; real-IRIS 2026.1 validated |
| M2 | sync (8 verbs) | ☑ | diff/rm/push --from/bare-name filter; real-IRIS green (404 + PutDoc bugs fixed) |
| M3 | exec (load/run/eval/abort) + engineError | ◐ | **next** — wire the remote runner Transport (already spiked) into exec; IRIS fault→§7; `--prefix`. Then build local/docker `iris session` transports (unblocks docker/local lifecycle up/down). |
| M4 | data (get/set/kill/query/export/import) | ☐ | remote via runner, SQL-wrapped |
| M5 | cover (%Monitor.LineByLine → LCOV) | ☐ | port mcov.FromMonitor |
| M6 | admin (backup/restore/check/journal) | ☐ | |
| M7 | native passthrough (iris/atelier/sql) | ☐ | |
| M8 | conformance green local+docker+remote | ☐ | release gate |

**needs SDK:** (record here any shared shape M3+ requires that isn't in the pinned
SDK yet, for the coordinator to batch — none currently; M3 exec uses v0.2.0's
`Exec`/`EngineError`.)
