---
name: m-iris-driver-m0-spike
description: "m-iris (IRIS driver D1) M0+M1+M2 done; Atelier-SQL runner substrate + lifecycle/health/doctor (remote) + sync axis complete (8 verbs). Next M3 exec via the remote runner."
metadata: 
  node_type: memory
  type: project
  originSessionId: b359cfba-9771-4992-b8ba-cefda6136bfe
---

m-iris (engine driver **D1**) lives at **`~/vista-cloud-dev/m-iris`** (dir renamed from
irissync 2026-06-04); module `github.com/vista-cloud-dev/m-iris`, binary `m-iris`, env
`M_IRIS_*`. GitHub repo renamed irissync→**`vista-cloud-dev/m-iris`** (public; old URL
auto-redirects). Branch `m-iris-driver` pushed (PR not yet opened to main).
Seeds from irissync's Atelier client. See repo `docs/m-iris-driver-status.md`.

**M0 done (test-first, race-clean):** `internal/driver` vendors the contract thin —
`CapsDoc()` (honest caps golden, advertises only wired verbs), `ContractVersion`,
verb-level `Transport` (Exec/Load/ReadGlobal/SetGlobal/Health) + `FakeTransport`.
clikit exit ladder aligned to contract `0/2/3/4/5/6/7` (runtime moved 1→5; added
ExitUnreachable=6, ExitUnsupported=7) + `clikit.EngineError` envelope field (§7).
Command tree regrouped to `m-iris <axis> <verb>`: `meta` (caps/info/version/schema)
+ `sync` (the old list/pull/status/verify/push/deploy).

**Remote spike done at unit tier (risk B2 — the whole remote substrate):** Atelier
has no run-ObjectScript endpoint, so all remote work rides `m_iris.Runner`
(`internal/remote/runner/m_iris.Runner.cls`) — role-gated parameterized SqlProc
methods, faults → `^mIrisRun(rid,"error")` in §7 shape. `atelier.Query` =
`POST {ns}/action/query`. `internal/remote.Transport` (implements driver.Transport)
lazily PUT+compiles the runner, then Exec/data/health over SQL. Fake-API unit tests
green every commit; `TestRemoteSpike_RealEngine` is **gated** (`M_IRIS_IT=1` +
`M_IRIS_*` env) and **not yet run green** — needs a provisioned IRIS CE container in
CI. The shared dev `vista-iris` container is OFF-LIMITS (docker exec denied).

**M1 done (remote/attach, test-first):** `atelier.ServerInfo` (GET root →
version/api/namespaces) + typed `*HTTPError` (`IsUnauthorized`/`IsForbidden`,
401≠403). `--transport` flag (default remote; only remote wired). `lifecycle` axis:
status/`--probe`(exit 0/6)/`wait`(exit 6 timeout)/up/down/restart; provision+destroy
report unsupported exit 7 over Atelier (risk B4 — attach mode). `meta doctor`: typed
matrix (reachable/auth/version≥2022.1/namespace/query-privilege via action-query
SELECT 1/license-not-probed), exit 0/5/6. caps grew lifecycle+doctor (still honest).
Commits on branch `m-iris-driver`: 8d1a3a7 (M0+spike), 9180e1b (M1).

**M2 DONE (2026-06-05) — committed+pushed `m-iris` b0531ed on `m-iris-driver`.** Plan §5 task 6 ☑.
Sync axis now 8-verb parity with m-ydb. Added: `sync diff <name> [--from DIR]` (instance GET,
Stat-gated absent→pure add/del, vs mirror/--from; `{unified}` via new `internal/udiff` ported
byte-identical from m-ydb); `sync rm <name>` (DeleteDoc + mirror + manifest; `--dry-run`;
already-absent reported not error; `{removed}`); `push --from DIR` (pushes any dir incl. fresh
creates — staged into mirror so the conflict/lock/compile path is unchanged; up-to-date check
reads the --from copy so dry-run is accurate); **bare-name `--filter`** (glob on ext-stripped name
via new `match`/`bareName` in commands.go; `*.mac` no longer matches). caps advertises all 8 sync
verbs (golden regen); meta schema auto-includes diff/rm. SDK UNCHANGED (these are driver-local
shapes already in contract §5.2) — both drivers still pinned m-driver-sdk v0.2.0. Unit tier green
(-race/vet/gofmt). Gated `TestSyncAxis_RealEngine` (pkg main, `make test-it` now runs `. + remote`)
round-trips an ephemeral `zzMIRISIT` routine.

**REAL-IRIS VALIDATED 2026-06-05 (commit 8c2f010): GREEN against live IRIS CE 2026.1** — the gated
sync round-trip caught **2 latent Atelier-client bugs the fake tier missed** (see
[[m-engine-drivers-real-engine-testing]] for the 2026.1 facts): (1) missing doc = HTTP 404 →
`isNotFound` now accepts 404 (Stat/DeleteDoc no longer hard-error on absent routines — affected
push conflict-check + diff + rm); (2) `.mac` PUT without a `ROUTINE name [Type=MAC]` header is
rejected #16021 as HTTP 200 with the reason in per-doc `result.status` (empty status.errors[]) →
`PutDoc` was silently "succeeding" without storing; now decodes result.status and errors. Unit
regression guards added (TestPutDocRejectedByStatus, TestStatMissing404). m-ydb real tier also
re-confirmed green vs YottaDB r2.07 same session.

**Next:** M3 (wire the remote Transport into exec load/run/eval/abort + IRIS fault→§7 engineError +
`--prefix`; then build local/docker `iris session` transports — the deferred lifecycle up/down for
docker/local rides those). m-ydb is already at M3 done, so m-iris M3 closes the exec-parity gap.

**Why:** the spike de-risks every remote feature at once; they all ride this one path.

**REAL-IRIS VALIDATION (2026-06-04 — see [[m-engine-drivers-real-engine-testing]]):** first-ever run
against a real IRIS (disposable `m-test-iris`, intersystemsdc/iris-community **2026.1**, port 52774,
_SYSTEM/testsys). Remote spike + M1 status/doctor now GREEN. Fixed **4 latent bugs the fake tier
missed**: (1) runner class `m_iris.Runner` invalid — IRIS forbids underscores in class names
(#16006) → renamed `m.iris.Runner` (package m.iris → SQL schema m_iris, so m_iris.* SQL unchanged);
(2) Atelier error `code` is a NUMBER on 2026.1 (client typed string) → added `errCode`; (3) runner
ObjectScript spaces-after-commas → #1043 "QUIT argument not allowed" → removed comma-whitespace,
catch uses `do ..fault()`; (4) `ServerInfo` hit `/api/atelier/v1/` (404 on modern IRIS) → use
unversioned `/api/atelier/`. `make test-it` runs the gated tier. Committed a502071 on m-iris-driver.
NOTE: docs/m-iris-driver-status.md still says "ServerInfo GET /api/atelier/v1/" — now stale, fix later.

**Phase-0 SDK freeze — DONE (2026-06-04, see [[m-driver-sdk-phase0]]):** the Transport was
frozen + extracted into `m-driver-sdk` (pkg `mdriver`) and m-iris switched onto it (commit
2d13d46 on `m-iris-driver`). Deleted `internal/driver/{transport,fake,transport_test}.go`;
caps.go `Caps` map→`mdriver` struct (honest set unchanged, golden regenerated);
`internal/remote` + meta retargeted to `mdriver`; `readEngineError`→`*mdriver.EngineError`
(behavior unchanged). go.mod `replace …/m-driver-sdk => ../m-driver-sdk`. Frozen verbs:
`Health·Load·Exec·ReadGlobal·SetGlobal` — m-ydb's `Compile`/`ExecMode`/flat `GlobalResult`
dropped; SetGlobal + GlobalNode tree + field-based Exec kept. `EngineError` now lives in the
SDK (clikit keeps its own for the envelope; convert at the boundary). All tests green;
`TestRemoteSpike_RealEngine` still gated. Spike assumptions still to confirm against a real
engine: SqlProc name `m_iris.<Method>`, `%Exception.Location` = `label+offset^routine`,
`@ref` indirection. Part of [[m-engine-drivers-project]].
