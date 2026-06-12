---
name: m-iris-exec-axis-t0a5
description: m-iris exec axis (load/run/eval) wired over the remote runner + the device-output capture machinery that closed VSL M0a's T0a.5 IRIS driver-path on foia. Has the hard-won KIDS-over-Atelier device-corruption findings.
metadata:
  node_type: memory
  type: project
---

**M0a T0a.5 driver-path PROVEN on IRIS FOIA (foia) 2026-06-12.** `v pkg
install/verify/uninstall <kid> --engine iris --transport remote` runs the full
KIDS lifecycle over the m-iris driver — all 3 invariants green, deterministic
across repeated runs: #9.7 status piece-9 = 3, `$$PING^ZZSKEL()`→"pong" (routine
loaded; verify `routines:{ZZSKEL:true}`), reversible uninstall (post-uninstall
verify = not installed). This + the already-green YDB driver-path = **M0a done**.
Branch `m-iris-driver`; SDK still pinned **v0.2.0** (everything additive in m-iris,
no SDK change, no frozen-SDK window).

## The exec axis (closes the gap that made install silently no-op)
Root cause of the original no-op: m-iris did NOT implement the neutral `exec` axis
the SDK reference `Client` shells to (`m-iris exec load/run` → `USAGE: unexpected
argument exec`, exit 2, which the Client swallowed). Fixed, all in m-iris:
- **`exec.go`** — `execCmd{Load,Run,Eval}` mounted as `Exec` in the `CLI` struct
  (main.go), mirroring m-ydb. `load` → `remote.Transport.Load`; `run`/`eval` →
  `remote.Transport.Exec`. `caps.go` advertises `Exec:["load","run","eval"]`
  (abort NOT wired). Caps golden regenerated (`UPDATE_GOLDEN=1`).
- **`.m`→`.int` docname** (`irisDocname`) — the neutral `.m` extension the SDK/v-pkg
  stage is not an Atelier routine type; map to `.int` (classic MUMPS, matches the
  routine-wrap label+space-code body).
- **UDL routine header REQUIRED** (`irisRoutineLines`) — Atelier rejects a routine
  PUT whose first line is a label, with `ERROR #16021: Illegal Header Line`. Prepend
  `ROUTINE <name> [Type=INT|MAC|INC]` (idempotent; skips a doc that already leads
  with `ROUTINE `, and `.cls`). **The fake tier missed this — only the live engine
  caught it** (encoded back into the fake's `PutDoc` as a #16021 guard).

## Device-output capture (the deepest part — 4 layered findings)
Atelier/SQL has no principal device a script's `W` output can be read from. The
runner now captures it. Each layer below was a separate live-only failure:

1. **Capture via `%Device.ReDirectIO` + a companion `.int` routine.** `RunRef`/`Eval`
   bracket `do @ref`/`xecute` with `start^mIrisIO`/`stop^mIrisIO`. start() turns on
   `##class(%Device).ReDirectIO(1)` and `use $io::("^mIrisIO")` — every WRITE then
   dispatches to mIrisIO's `wstr`/`wchr`/`wnl`/`wff`/`wtab` labels, which append to
   `^mIrisRun(rid,"out")`. **A class method CANNOT host mnemonic-space labels**, so
   mIrisIO is a separate `.int` routine, deployed + compiled with the class by
   `ensureRunner`. stop() must NEVER throw (try/catch) and restores the device's
   ORIGINAL mnemonic (saved via `GetMnemonicRoutine`) — else the framework's later
   writes dispatch into mIrisIO.

2. **KIDS `EN^XPDIJ` corrupts the Atelier SQL-gateway device.** It issues
   `USE IO:(params)` (terminal mode) that ReDirectIO does NOT intercept, so the
   action/query **RESPONSE BODY is lost: HTTP 200 with an empty body** (proven with
   an in-IRIS `%Net.HttpRequest` probe). The Go atelier client then errors
   ("HTTP 500"-ish). **The run still COMPLETES** — globals are set, #9.7 reaches 3.
   `write *-3` / mnemonic-restore did NOT fix the lost body.

3. **Recover the outcome from globals, not the response.** Runner `RunRef`/`Eval`
   record `status`/`out`/`error` in `^mIrisRun(rid,*)` and set **`"done"` LAST**.
   `Transport.Exec` ignores the (possibly-lost) response and reads the outcome from
   the globals, gating on `"done"` (missing → the run truly didn't run).

4. **Binary-safe + fresh-connection reads.** The captured `out` has control bytes
   (ANSI/ESC/CR from KIDS) that mangle/truncate over action/query JSON — so a new
   runner method **`GetOut(rid)` Base64-encodes** it (`$system.Encryption.Base64Encode`;
   Go strips whitespace before decoding). AND the corrupted gateway process keeps
   spoiling responses on the same keep-alive connection — so `recoverRun` calls
   **`CloseIdleConnections()` and RETRIES** (up to ~2s) so a fresh connection lands
   on a clean process. Both were necessary; either alone still failed.

**JOB-isolation was tried and rejected:** running the install in a JOB'd child
keeps the SqlProc's device clean (no lost body) BUT the child's redirect drops at
`XPDIJ`'s end (its principal differs), truncating capture before the marker. Inline
capture is complete; the global-recovery path handles the lost response.

## Gotcha — corrupt half-installs poison the next install (cost real time)
Aborted/500'd install runs leave `#9.7 "B"` xref entries (written by
`$$INST^XPDIL1` before `EN^XPDIJ` completes). The install script's
`I $D(^XPD(9.7,"B",name))` guard then fires "already-installed", and uninstall's
`$O`+`DIK` removes only ONE entry. Symptom: install reports `status=0` /
already-installed on a "clean" system. **Purge by IEN** before a clean run:
`F  S da=$O(^XPD(9.7,"B",name,"")) Q:da=""  K ^XPD(9.7,da),^XPD(9.7,"B",name,da),^XPD(9.7,"ASP",da),^XTMP("XPDI",da)`
(+ #9.6 B). See the cleanup routine pattern in [[t0a3-live-install-handoff]].

## Gates (all green 2026-06-12)
`go test -race ./...`, gofmt, vet, golangci-lint (no new findings) ✅; `make test-it`
vs foia (TestSyncAxis / TestRemoteSpike / **TestRemoteExecAxis** RealEngine) ✅; SDK
conformance 16/0 vs the rebuilt m-iris (remote) ✅. New: `exec_test.go` (command tier),
`TestLoad_MapsDotMToIntDocname` + header guard, `TestRemoteExecAxis_RealEngine`
(load→run→read-stdout). See [[m-iris-driver-m0-spike]], [[m-iris-public-facade]].

**Flipping the shared VSL `T0a.5` row to ☑ in m-stdlib's
`docs/tracking/vsl-implementation-tracker.md` is a coordinator/v-pkg-session
action** (not this driver lane).
