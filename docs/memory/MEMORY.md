# Memory index — m-iris (IRIS engine driver, D1)

Driver-local memory for the **m-iris** repo. A session started in
`~/vista-cloud-dev/m-iris` recalls from here (the harness memory path is symlinked
to this dir). Write m-iris-specific facts here — NOT to `~/claude/memory` and NOT
to the `docs` repo's `docs/memory/`.

Cross-repo coordination (the consistency protocol, the SDK version ledger, the
driver contract, the frozen-SDK-window rhythm) lives in the **`docs` repo's
`docs/memory/`** + the org/per-repo `CLAUDE.md` — those load as rules; read them
for how m-iris stays in lockstep with m-ydb via `m-driver-sdk`.

- [m-iris driver M0–M2 + remote spike](m-iris-driver-m0-spike.md) — IRIS driver (D1), branch `m-iris-driver`. M0+M1+M2 done — sync axis 8-verb parity (diff/rm/push --from/bare-name filter); real-IRIS-2026.1 validated (404 + PutDoc result.status bugs fixed, 8c2f010). Atelier-SQL runner substrate gated. Next M3 exec. Pins m-driver-sdk v0.2.0.
- [m-iris public facade](m-iris-public-facade.md) — NEW `irisdriver.New(Config)→mdriver.Transport` for m-cli/VistaEngine (peer of m-ydb's ydbdriver). Live-validated vs m-test-iris (banner returned). NOTE: the old "IRIS Exec does NOT capture device `W`" rule is **superseded** by [[m-iris-exec-axis-t0a5]] — the runner now redirects device output into `^mIrisRun(rid,"out")`.
- [exec axis + T0a.5 driver-path](m-iris-exec-axis-t0a5.md) — **M3 DONE (2026-06-12)**: full exec axis (load/run/eval/abort) over BOTH remote (Atelier runner) and the new docker/local `iris session` transport; conformance 16/16 on remote AND docker. Has: the remote runner device-`W` capture + KIDS-over-Atelier corruption recovery; the session single-line TRY/CATCH `@@MIRIS-BEGIN/RESULT@@` capture protocol (`$ZTRAP` won't cross stdin-prompt lines); `exec abort` (`$job`+`^$JOB`+`Process.Terminate`); `transport.go` selector. T0a.5 (`v pkg … --engine iris`) proven on foia. SDK still v0.2.0.
