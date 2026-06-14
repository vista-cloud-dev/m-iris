# m-iris driver — milestone status

Tracking the [driver-implementation-plan §5](../../docs/m-engine-drivers/driver-implementation-plan.md)
table inside this repo (the plan doc itself is shared read-only source of truth).

Legend: ☑ done · ◐ in progress · ☐ not started

## M0 — scaffold + SDK seam + meta ☑

- ☑ Rename `irissync` → `m-iris`: module `github.com/vista-cloud-dev/m-iris`,
  binary `m-iris`, env prefix `IRISSYNC_*` → `M_IRIS_*`. (Directory kept as
  `irissync/`; git remote unchanged.)
- ☑ Contract types vendored thin in `internal/driver` (caps, `ContractVersion`,
  the verb-level `Transport` seam, `clikit.EngineError` + envelope field).
- ☑ clikit exit ladder aligned to the contract: `0/2/3/4/5/6/7`
  (added `ExitRuntime=5`, `ExitUnreachable=6`, `ExitUnsupported=7`).
- ☑ Axis command tree `m-iris <axis> <verb>`: `meta` (caps/info/version/schema)
  + `sync` (the regrouped source verbs). `caps` golden-tested and **honest**
  (advertises only what is wired; grows per milestone).

## M1 — lifecycle + health probes + doctor ◐ (remote done; local/docker pending)

- ☑ `atelier.ServerInfo` — `GET /api/atelier/v1/` → version / api / namespaces;
  typed `*HTTPError` with `IsUnauthorized`/`IsForbidden` (401 vs 403 distinct).
- ☑ `--transport local|docker|remote` flag (default `remote`; only remote wired).
- ☑ `lifecycle` axis (remote/attach): `status` (+`--probe` CI gate, exit 0/6),
  `wait --timeout` (poll → exit 6 on timeout), `up` (verify+attach), `down`
  (detach no-op), `restart`; `provision`/`destroy` report **unsupported (exit 7)**
  over Atelier (risk B4). local/docker → not-implemented until M3 transports.
- ☑ `meta doctor` — typed matrix {name,ok,detail,fix}, exit 0/5/6: reachable,
  auth (401/403), version (≥ 2022.1), namespace presence, **query-privilege**
  (action/query SELECT 1 — the C7 runner-privilege proxy), license (honestly
  not-probed on remote until M6).
- ☐ local/docker lifecycle (container / `iris start`/`iris stop`) — needs the
  session-transport command seam (lands with M3 local+docker exec).

## M2 — sync axis ☑ (plan §5 task 6)

The sync axis reaches 8-verb parity with m-ydb. The irissync source verbs were
already regrouped under `sync` in M0 (`list`/`pull`/`status`/`verify`/`push`/
`deploy`); M2 adds the inspect/delete/author verbs and tightens the filter.

- ☑ `sync diff <name> [--from DIR]` — unified diff of the instance copy (GET
  over Atelier, gated by `Stat` so an absent routine diffs as a pure
  addition/deletion) vs the local mirror, or a `--from` directory. `{ unified }`.
  Diff engine is `internal/udiff` (LCS, single hunk, 3-line context), ported
  byte-identical from m-ydb.
- ☑ `sync rm <name>` — removes a routine from the instance (`DeleteDoc`), the
  mirror, and the manifest; honors `--dry-run`. `{ removed }`. A routine already
  absent is reported, not an error.
- ☑ `push --from DIR` — pushes routines from an arbitrary directory (incl. fresh
  creates the manifest has never seen). Content is staged into the mirror, so
  push's conflict-check / single-writer lock / compile-on-import path runs
  unchanged; the up-to-date short-circuit reads the `--from` copy so `--dry-run`
  is accurate.
- ☑ Bare-name `--filter` — the glob matches the extension-stripped routine name
  (`DG*`/`DGREG` select `DGREG.mac`; `*.mac` never matches), parity with m-ydb
  `source.Match` and driver-contract §5.2.
- ☑ `caps` advertises all 8 sync verbs (honest gate; golden regenerated); the
  `meta schema` tree picks up diff/rm automatically.
- ◐ Real-engine tier: `TestSyncAxis_RealEngine` (package `main`, gated on
  `M_IRIS_IT=1` + `M_IRIS_*`, `make test-it`) pulls/pushes/diffs/removes an
  ephemeral `zzMIRISIT` scratch routine against a live IRIS, self-cleaning. The
  verbs ride the GET/PUT/DELETE doc + docnames endpoints already validated
  against IRIS CE 2026.1 in M1; the gated round-trip was not executed this
  session (the disposable `m-test-iris` container was down and docker/curl were
  sandbox-blocked) — run `make test-it` once the container is up.

## Remote spike (plan §5 task 8) — substrate built, real-engine green gated ◐

The remote substrate is the whole-cloth de-risking item (risk B2): Atelier has no
"run ObjectScript" endpoint, so all remote exec/data/cover/admin ride a SQL
runner class. Built and **unit-proven**; real-engine green runs in CI.

- ☑ `m_iris.Runner` class (`internal/remote/runner/m_iris.Runner.cls`):
  role-gated, parameterized SqlProc methods `RunRef`/`Eval`/`GetGlobal`/
  `SetGlobal`/`KillGlobal`/`Ping`; faults captured to `^mIrisRun(rid,"error")`
  in §7 shape.
- ☑ `atelier.Query` — `POST {ns}/action/query` (SQL), parameter-bound.
- ☑ `internal/remote.Transport` implements `driver.Transport` over the runner:
  lazy PUT+compile deploy, `Exec` (run/eval) with fault→`EngineError`, data
  set/get, `Health` probes the action/query privilege (SELECT 1, not just TCP).
- ☑ Unit tier (fake `AtelierAPI`, runs every commit): deploy-once, clean run,
  fault→EngineError, data round-trip, health.
- ◐ Real-engine tier: `TestRemoteSpike_RealEngine`, gated on `M_IRIS_IT=1` +
  `M_IRIS_*` env. **Not yet run green** — needs a provisioned IRIS CE container
  in CI (the shared dev `vista-iris` is off-limits).

### Spike assumptions to confirm on the real engine
- SqlProc projection name is `m_iris.<Method>` (schema = package `m_iris`).
- `%Exception.Location` parses as `label+offset^routine` for the §7 frame.
- `do @ref` / `set @ref=value` name-indirection over a global reference string.

## Next
- **M3 exec** — wire the remote runner Transport (already built + spiked) into
  `exec load`/`run`/`eval`/`abort`; parse IRIS faults into the §7 `engineError`;
  `--prefix` ephemeral runs. Then build the `local`/`docker` (`iris session`)
  Transport strategies (which also unblock the deferred docker/local
  `lifecycle up`/`down`).
- Phase-0 SDK reconciliation with m-ydb (see below).

## SDK reconciliation note (Phase-0)
m-ydb drafted the seam first (`internal/transport`, separate `internal/contract`
pkg, `ExecMode` enum, flat `GlobalResult`, **no** `SetGlobal`). m-iris's
contribution: the Atelier-SQL fit (results via a global, not stdout) and the
**write** verbs (`SetGlobal`/kill) the data axis needs. Freeze + extract
`m-driver-sdk` against both shapes before building broad M3 work.
