---
name: m-iris-exec-axis-t0a5
description: "m-iris exec axis (load/run/eval) wired over the remote runner + the device-output capture machinery that closed VSL M0a's T0a.5 IRIS driver-path on foia. Has the hard-won KIDS-over-Atelier device-corruption findings."
metadata: 
  node_type: memory
  type: project
  originSessionId: 70bf5dbe-39a1-44d9-9439-a19b1fdfbe39
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

## M3 DONE — docker/local `iris session` transport (added 2026-06-12)
`internal/session` implements `mdriver.Transport`+`Abort` over `iris session
<instance> -U <ns>` (docker = `docker exec -i <container> iris session …`; local =
bare, host-unvalidated — no host IRIS here). **Device `W` captured DIRECTLY off the
principal device** — none of the remote/Atelier mIrisIO-redirect or
global-recovery machinery is needed (that whole mess is a remote-only problem).
**`transport.go` selector** `newExecTransport(conn)→execTransport` (=`mdriver.Transport`
+`Abort`) picks remote vs session; exec/lifecycle/doctor are now transport-agnostic
(status/up/down/restart/wait + doctor dispatch to a session probe; docker `up`=
`docker start`+wait-healthy, `down`=`docker stop`; remote/local `down`=detach no-op).
New config: `--container`/`M_IRIS_CONTAINER`, `--iris-instance`/`M_IRIS_IRIS_INSTANCE`
(default `IRIS`). **Conformance 16/16 on BOTH remote AND docker.** Live tier
`TestSessionAxis_RealEngine` (gated `M_IRIS_IT=1`+`M_IRIS_CONTAINER`; `make test-it`
now runs `. ./internal/remote/ ./internal/session/`).

**Capture protocol (live-proven, the crux):** an `iris session` reading stdin runs
each line independently at the `USER>` prompt — so a **`$ZTRAP` set on a prior line
does NOT fire** (the error prints inline and the next line still runs). Fault capture
therefore uses a **single-line TRY/CATCH** in the same physical line as the code:
`write "@@MIRIS-BEGIN@@",! set st=0,em="" try { xecute mcmd } catch ex { set st=5,em=
ex.Name_"|"_$piece(ex.Location,"^",2)_"|"_… } write "@@MIRIS-RESULT@@",st,"|",em,! halt`.
Parser takes text between BEGIN and RESULT as stdout, then `<st>|<mnem|rtn|line|text>`.
User cmd carried as an escaped ObjectScript string literal (double the `"`) and
`xecute`'d (so a syntax error is caught, not a wrapper crash). Load = pipe source into
the container (`docker exec -i … sh -c 'cat > /tmp/X.int'`) / host temp file for local,
then `$system.OBJ.Load(path,"ck")` (compile fault → LoadResult.EngineError). ReadGlobal
Base64-encodes the value (control-byte safe, like remote GetOut).
**De-flake note:** `TestRemoteAbort_RealEngine` was flaky ("run never registered a
pid") under concurrent runner PUT+compile from two transports — fixed by pre-deploying
the runner on both (a `ReadGlobal` calls `ensureRunner`) before the timing-sensitive poll.

## exec abort (added 2026-06-12 — closes the last exec verb)
Abort over the synchronous Atelier path needs a live target, so the runner now
records its OWN `$job` into `^mIrisRun(rid,"pid")` (set right after `status`, and
"done" — set last — marks completion). New `m_iris.Abort(rid)` SqlProc: terminates
the recorded pid via `$system.Process.Terminate(pid,2)` **iff** pid set ∧ no "done"
∧ pid≠`$job` (never self) ∧ `^$JOB(pid)` defined (process still live); returns the
pid, `""` (nothing live — the common case, parity with m-ydb "no jobs matched"), or
`"DENIED"` (role-fail). `remote.Transport.Abort(ctx,prefix)→[]string` (driver-local,
NOT an SDK `Transport` verb — same as m-ydb's `Session.Abort`); `exec abort --prefix`
in exec.go; caps Exec `[load,run,eval,abort]`. **Live-proven** `TestRemoteAbort_
RealEngine` on m-test-iris: two transports — one runs `hang 30`, the other aborts by
prefix, gets the pid, the blocked call returns, second abort finds nothing.
**IRIS gotchas (live-caught):** `$ZCHILD` is a YottaDB-ism — `<SYNTAX>` in IRIS (so
capture the run's own `$job`, not a JOB'd child's); `^$JOB(pid)` is the M-native
liveness check; `$system.Process.Terminate(pid,2)`→1 and the process dies.

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

5. **Wide-char (Unicode >255) output (added 2026-06-13).** Finding 4's
   `$system.Encryption.Base64Encode` requires an **8-bit byte string** — it faults
   `<ILLEGAL VALUE>GetOut+2^m.iris.Runner.1` on a captured value holding a char
   >255. The capture path makes that easy to hit: `wchr(c) do app($char(c))`, so a
   script's `W $C(8212)` (em-dash) appends a 16-bit char and the whole `out` global
   becomes a wide string. Surfaced on the VSL T0b.2 IRIS test-in-place leg: m-stdlib
   suites whose PASS-line descriptions are non-ASCII (STDURL/STDREGEX/STDJSON/STDXML)
   **errored with no result frame** over the remote path. **Fix: `GetOut` now
   `$zconvert(...,"O","UTF8")` BEFORE Base64** — UTF-8 leaves ASCII/≤127 bytes
   identical (KIDS marker path byte-unchanged, exec-axis IT still green) and emits
   multi-byte sequences for the rest; Base64 is then always byte-safe. The Go
   `getOut` is **unchanged** — `string(raw)` of the Base64-decoded UTF-8 bytes is
   already the correct Go (UTF-8) string. Proven by `TestRemoteWideChar_RealEngine`
   (`W "<<W>>",$C(233),$C(8212),"end"` → Stdout contains `<<W>>é—end`; trailing
   marker survives — the exact suite failure mode). **NB:** this is the **remote
   (Atelier) transport** only; the docker/session transport captures via `iris
   session` stdout markers (a separate path), so a wide-char issue there (if any) is
   independent of this fix. **Downstream confirmation owed:** re-run
   `kids-test-in-place.sh iris` on foia (remote) — the 4 suites should now produce
   frames; m-cli's `m test` has no remote-IRIS transport, so they can't be re-run
   over remote through the runner here.

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

**Flipping the shared VSL `T0a.5` row to ☑ in the `docs` repo's
`docs/vsl-msl/vsl-implementation-tracker.md` is a coordinator/v-pkg-session
action** (not this driver lane).
