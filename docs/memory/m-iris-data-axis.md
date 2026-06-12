---
name: m-iris-data-axis
description: m-iris M4 data axis (get/set/kill/query) over both transports — the $query subtree-walk + the $name(@cur) containment gotcha, and the shared node-list wire format.
metadata:
  type: project
---

**M4 data axis get/set/kill/query — DONE 2026-06-12 (export/import still ☐).**
`data.go` (`dataCmd`) mounts the axis; caps `Data:[get,set,kill,query]`. All verbs
ride `engineTransport` (transport.go: `mdriver.Transport` + driver-local `Abort`,
`KillGlobal`, `QueryGlobal`), so they work identically on remote (runner SqlProcs)
and local/docker (`iris session`). Conformance 16/16 on remote AND docker; live
tiers `TestRemoteData_RealEngine` + the session-axis query/kill block. See
[[m-iris-exec-axis-t0a5]] for the transport selector + session capture protocol.

## The hard-won gotcha — `$name(cur)` vs `$name(@cur)` (cost ~5 live probes)
The subtree-query walk uses `$query` + a containment test. The containment is a
SINGLE expression: a node `cur` is in `ref`'s subtree iff
**`$name(@cur,$qlength(ref))=ref`** — `$name(glvn,depth)` truncates a reference to
its first `depth` subscripts. THE BUG: `$name(cur,…)` operates on the *variable*
`cur` (returns "cur"/garbage), so it must be **`$name(@cur,…)`** to indirect cur's
string value into a real reference first. (Inconsistent with `$qsubscript`, which
takes the string-reference DIRECTLY with no `@` — that asymmetry is what burned
time.) With the `@`, `$name(@cur,1)` on `^X(1,"y")` → `^X(1)`. Collation makes a
subtree contiguous in `$query` order, so the walk `quit`s as soon as containment
first fails. `bl=0` (bare `^X`) → `$name(@cur,0)` = the global name → whole-global
walk. Validated live: query `^mDTST(1)` returns only `^mDTST(1)`+`^mDTST(1,"x")`,
excludes `^mDTST(2)`.

## Shared node-list wire format (both transports → one Go parser)
Query returns nodes as **`Base64(ref)<TAB>Base64(value)<LF>` per node** (base64 each
field so control bytes survive; TAB/LF framing is plain text). Remote: runner
`m_iris.QueryGlobal(ref,order,depth)` SqlProc returns the whole string (read from the
action/query row). Session: the inline `$query` walk **writes each node to the
principal device** (captured between the `@@MIRIS-BEGIN@@`/`@@MIRIS-RESULT@@` markers
like any session command). `parseNodes` (duplicated in internal/remote + internal/
session) splits lines → split on TAB → base64-decode each → `[]mdriver.GlobalNode`.

## Session $query walk — interactive-mode shape
Piped `iris session` runs each stdin line independently at the prompt, BUT **locals
DO persist across lines** (set `qref/qdir/qbl` on one line, use on the next — works).
A `for { … }` BLOCK piped interactively did NOT iterate (only the pre-for base node
emitted) — so the session walk uses the **argumentless-body `for` form**
(`for  set qcur=$query(@qcur,qdir) quit:… quit:… write:<depthPC> <node>`) with the
RESULT marker on the NEXT line (so it isn't swept into the FOR body). depth filter is
a write postconditional `'((qd>0)&(($qlength(qcur)-qbl)>qd))` (fully parenthesized — M
is strict left-to-right, no operator precedence). kill = `kill @(<ref>)`.

## Remaining: export/import (deferred, its own slice)
`data export <pattern> --to` / `data import <file>` → `{bytes}`/`{loaded}`. Heaviest:
server-side dump files (`%Library.Global.Export` / `%GO`/`%GI`), and for remote the
file lands on the SERVER (client can't easily retrieve) — design the
where-does-the-file-live semantics before wiring. NOT advertised in caps until wired
(honest-by-construction). **needs SDK:** none — these are driver-local CLI result
shapes (`{bytes}`/`{loaded}`), not Transport-seam types.
