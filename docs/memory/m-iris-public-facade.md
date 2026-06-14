---
name: m-iris-public-facade
description: m-iris gained a public irisdriver.New facade returning mdriver.Transport for m-cli/VistaEngine; plus the IRIS Exec result-global capture rule.
metadata:
  type: project
---

m-iris now exposes a public **`irisdriver`** package (`irisdriver/irisdriver.go`):
`New(Config) (mdriver.Transport, error)` with `type Config = atelier.Config`. It
composes `atelier.New` + `remote.New` so an external module (m-cli's
**VistaEngine**) can hold an IRIS `mdriver.Transport` without importing m-iris
`internal/`. Construction is lazy — no dial until the first verb (the runner class
is PUT+compiled on first use). This is the symmetric peer of m-ydb's
[[m-ydb-remote-ssh-transport]] `ydbdriver`: both drivers now have a public
constructor returning the neutral contract, so VistaEngine unifies IRIS (Atelier
REST :52773) and YottaDB (SSH / local / docker) behind one `Transport`.

**Live-validated** against `m-test-iris` (IRIS CE 2026.1, host :52774,
_SYSTEM/testsys, ns USER) via a gated `M_IRIS_IT=1` facade test
(`irisdriver/irisdriver_it_test.go`): New → Health (privileged SELECT 1) → Exec
returned the real banner *"IRIS for UNIX … 2026.1 (Build 234U)"*. (HTTP path —
reachable here; docker-exec/ssh stay blocked.)

**Cross-engine capture rule (important for VistaEngine).** The IRIS runner
`Eval` does `xecute cmd` with **no IO redirection**, so device `W` output is NOT
captured — `Exec("W $ZV")` yields empty Stdout. A command must write its result
into `^mIrisRun(rid,"out")` (e.g. `set ^mIrisRun("zzv","out")=$zv`), which
`remote.Exec` reads back as `ExecResult.Stdout`. YottaDB, by contrast, captures
the yottadb session's device stdout directly. **Consequence:** the unified
readiness/version probe across engines is `Transport.Health()` (carrying
`Version`), not `Exec("W $ZV")`. The runner has a `Ping()→$zversion` method and
IRIS also exposes version via the Atelier root (`ServerInfo`); wiring
`Health.Version` on both drivers is the clean unification (planned in the
VistaEngine increment).
