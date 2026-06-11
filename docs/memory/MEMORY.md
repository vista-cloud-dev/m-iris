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
- [m-iris public facade](m-iris-public-facade.md) — NEW `irisdriver.New(Config)→mdriver.Transport` for m-cli/VistaEngine (peer of m-ydb's ydbdriver). Live-validated vs m-test-iris (banner returned). KEY RULE: IRIS Exec captures the result-global `^mIrisRun(rid,"out")`, NOT device `W` output → unified probe is `Health()`+Version, not `Exec("W $ZV")`.
