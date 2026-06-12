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
| M3 | exec (load/run/eval/abort) + engineError | ◐ | **exec `load`/`run`/`eval` WIRED over the remote runner (2026-06-12)** — `exec.go` + `execCmd` mounted in `CLI`; caps advertises `exec`; IRIS fault→§7 engineError; the SDK reference `Client` now drives a live VistA over the seam. Device `W` output is now CAPTURED (see device-capture note below). **T0a.5 driver-path PROVEN on foia** (`v pkg install/verify/uninstall --engine iris` — all 3 M0a invariants green, deterministic). `--prefix` on run; `abort` + local/docker `iris session` transports still ☐. |
| M4 | data (get/set/kill/query/export/import) | ☐ | remote via runner, SQL-wrapped |
| M5 | cover (%Monitor.LineByLine → LCOV) | ☐ | port mcov.FromMonitor |
| M6 | admin (backup/restore/check/journal) | ☐ | |
| M7 | native passthrough (iris/atelier/sql) | ☐ | |
| M8 | conformance green local+docker+remote | ☐ | release gate |
| DRV | **public `irisdriver` facade** | ☑ | `New(Config)→(mdriver.Transport,error)` over Atelier REST + runner; the importable seam for in-process embedders (vendor logic stays internal/). **Live-validated vs m-test-iris (2026.1):** New→Health→Exec($zv via result-global) returns the IRIS banner. |
| CFM | **`meta version` conformance fix** | ☑ | Was the shared `clikit.VersionCmd` (`{version,commit,date,go}`) — non-conformant: contract §5.7 version = `{driver,engine,contract,build}` (caught by `m-driver-conformance`). Replaced with a driver-specific `versionCmd` emitting `{driver:"m-iris",engine:"iris",contract,build{…}}`; clikit untouched (byte-identical). **Conformance now 16/16 live vs m-test-iris (remote).** |
| CFM2 | **clikit `ResultExit` + doctor envelope/exit** | ☑ | Mirrored the shared clikit fix (byte-identical with m-ydb): `Context.ResultExit(data, exit, text)` so `meta doctor` emits its data envelope with the resolved exit (0/5/6) and `Run` returns `cc.ExitCode()`. doctor's unreachable path now emits `ok=false, exit=6` with process exit 6 (was the latent `cc.Result`-then-`Fail` stdout-exit-0 mismatch). Conformance stays 16/16 live. |

**Device-capture note (UPDATED 2026-06-12 — supersedes the old "no IO redirection"
note):** IRIS `Exec` now CAPTURES device `W` output. The runner's `RunRef`/`Eval`
bracket `do @ref`/`xecute` with `start^mIrisIO`/`stop^mIrisIO`, which turn on
`##class(%Device).ReDirectIO` and point the principal device's mnemonic space at
the companion `mIrisIO.int` routine; its `wstr`/`wchr`/`wnl`/`wff`/`wtab` labels
append every WRITE to `^mIrisRun(rid,"out")`, which `remote.Exec` returns as
`Stdout`. (A class method can't host mnemonic-space labels — hence the separate
`.int` routine, deployed + compiled alongside the class by `ensureRunner`.) This is
what lets v-pkg's `<<VPKG>>key=val` install markers flow back. **KIDS-install
caveat:** `EN^XPDIJ` reconfigures the Atelier SQL-gateway device with USE-params
ReDirectIO can't intercept, so the action/query RESPONSE BODY is lost (HTTP 200 +
empty body) even though the run completes; the runner therefore records
`status`/`out`/`error` in `^mIrisRun(rid,*)` and sets `"done"` LAST, and `Exec`
RECOVERS the outcome from those globals — Base64-encoded (`GetOut`) so control bytes
survive, retrying on fresh connections (`CloseIdleConnections`) until a clean
gateway process serves the read. `Health()`+Version remains the portable readiness
probe; `W $ZV` via `Exec` now also works on IRIS.

**needs SDK:** (record here any shared shape M3+ requires that isn't in the pinned
SDK yet, for the coordinator to batch — none currently; the facade + M3 exec use
v0.2.0's `Exec`/`EngineError`.)
