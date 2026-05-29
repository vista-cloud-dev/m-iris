# irissync

**The standalone binary that owns the IRIS source boundary in both
directions.** `irissync` materializes the M routines of an IRIS namespace into a
git-friendly mirror tree + a verifiable manifest (the **read** side), and writes
edited routines back to IRIS (the **`push`** side). The read verbs are **safe by
construction** — every IRIS operation is a read (`GET`) over the Atelier REST
API; the only thing they write is the local mirror. **`push` is the opt-in write
path and the sole DB writer**, gated so it can never clobber a change made
underneath it (see below).

It is a **self-contained binary** — configured entirely by flags + `IRISSYNC_*`
env (secrets optionally from files), with no dependency on the wider `m-cli`
suite. File-based tooling then consumes the mirror as ordinary files.

> **Two halves, one binary.** The **read / liberation** verbs — `list`, `pull`,
> `status`, `verify` — never touch IRIS source; run them against dev/test/pre-prod
> with zero risk. **`push`** (stage 2.1) is the write-back half and the **sole DB
> writer**: it PUTs edited routines and compiles-on-import, and is gated by a
> **single-writer lock + a manifest conflict-check + detect-and-defer** so the
> read-only safety story still holds — a write is refused (exit 4) rather than
> overwriting a routine that changed since you pulled (design:
> [`liberation-binary-design.md`](https://github.com/vista-cloud-dev/vista-dev-bridge/blob/main/docs/liberation-binary-design.md) §5).

```sh
export IRISSYNC_BASE_URL=https://host:52773/api/atelier/v1/
export IRISSYNC_NAMESPACE=VISTA
export IRISSYNC_INSTANCE=vehu-dev
export IRISSYNC_TYPE=int       # VistA routines are .int (^%RI-loaded); see "Routine type" below
irissync list                 # connectivity + inventory (no writes)
irissync pull                 # DB → .int mirror + manifest (incremental)
irissync status               # server vs. local manifest drift (exit 3 on drift)
irissync verify               # re-hash the mirror against the manifest
# edit routines in the mirror, then write them back (the sole DB writer):
irissync push --dry-run       # plan: what would be pushed / conflicts / deferred
irissync push                 # PUT + compile, single-writer-locked + conflict-checked
```

---

## Commands

| Command | What it does | Writes? |
|---------|--------------|:-------:|
| `list` | Print server routine docnames. Connectivity/auth smoke test + inventory. | no |
| `pull` | Materialize IRIS routine source → mirror, incremental via the manifest. | local mirror only |
| `status` | Diff server vs. local manifest: `new` / `changed` / `deleted` / `unchanged`. | no |
| `verify` | Re-hash mirror files against the manifest. Integrity gate for CI. | no |
| `push` | **Write edited routines back to IRIS** (PUT + compile-on-import). The sole DB writer; single-writer-locked + conflict-checked (exit 4 on refusal). | **IRIS** (gated) |
| `version` | Print version + Go toolchain (the pinned/mirrored audit trail). | no |
| `schema` | Emit the command/flag tree as JSON (agent discovery). | no |
| `install-completions` | Install shell tab-completions (bash/zsh/fish). | no |

## Configuration — flags and env

Config comes from **flags or `IRISSYNC_*` env** (flags win), with secrets
optionally read from files — so `irissync` is self-sufficient and needs no config
file of its own. It does not parse `.m-cli.toml`; an orchestrator like `m-cli`
*may* resolve per-instance profiles and pass values down, but that is optional,
never required ([design §4](https://github.com/vista-cloud-dev/vista-dev-bridge/blob/main/docs/liberation-binary-design.md)).

| Flag | Env | Default | Meaning |
|------|-----|---------|---------|
| `--base-url` | `IRISSYNC_BASE_URL` | — | Atelier base, e.g. `https://host:52773/api/atelier/v1/` |
| `--instance` | `IRISSYNC_INSTANCE` | — | instance label used in the mirror path |
| `--namespace` | `IRISSYNC_NAMESPACE` | — | IRIS namespace to liberate |
| `--mirror` | `IRISSYNC_MIRROR` | `.m-cache` | mirror root directory |
| `--type` | `IRISSYNC_TYPE` | `mac` | routine type: `mac` (UDL/ObjectScript), `int` (classic MUMPS — e.g. `^%RI`-loaded VistA), `inc` (includes) |
| `--token` | `IRISSYNC_TOKEN` | — | OAuth2/bearer token (`Authorization: Bearer …`); wins over `--user`/`--password` |
| `--token-file` | `IRISSYNC_TOKEN_FILE` | — | read the bearer token from a file (preferred — keeps it out of argv/env) |
| `--user` / `--password` | `IRISSYNC_USER` / `IRISSYNC_PASSWORD` | — | basic auth |
| `--password-file` | `IRISSYNC_PASSWORD_FILE` | — | read the password from a file (preferred over `--password`) |
| `--ca-file` | `IRISSYNC_CA_FILE` | — | internal CA bundle (PEM) for in-boundary TLS |
| `--client-cert` / `--client-key` | `IRISSYNC_CLIENT_CERT` / `_KEY` | — | mutual TLS |
| `--concurrency` | — | `8` | parallel document GETs |
| `--filter` | — | — | glob over docnames (e.g. `DG*`) |
| `--package` | — | — | restrict to a routine-name prefix |
| `--dry-run` | — | — | plan only; never write |
| `--porcelain` | — | — | terse, line-oriented output for `list`/`status` |
| `--full` (pull) | — | — | ignore the manifest; re-pull everything |
| `--force` (push) | — | — | push even if the server changed since pull / is held by another writer (override the conflict-check + detect-and-defer) |
| `--lock-ttl` (push) | — | `15m` | reclaim a stale push lock older than this |
| `--no-compile` (push) | — | — | skip the post-import compile (compile is on by default) |

`list` needs `--base-url` + `--namespace`; `verify` needs `--instance` +
`--namespace`; `pull`/`status`/`push` need all three.

## Enterprise & multi-instance auth

`irissync` is a **standalone, portable binary** — it liberates routines from an
IRIS system on its own, configured entirely by flags + `IRISSYNC_*` env (with
secrets sourced from files). It never depends on `m-cli`; an orchestrator like
`m-cli` is an *optional* convenience for resolving per-instance profiles, not a
requirement.

For a developer working against the VA's enterprise-licensed IRIS (PIV/CAC +
SSO, FedRAMP, on AWS) across many dev / test / pre-prod VistA systems, the model
that holds up:

- **Human path → bearer token.** A VA SSO (PIV-backed OIDC) token presented via
  `--token-file` (or `--token`/`IRISSYNC_TOKEN`) as `Authorization: Bearer …`;
  it **wins over** `--user`/`--password`. This is the realistic *human* path:
  a PIV/CAC private key lives on the smartcard and **cannot be exported to a PEM
  file**, so direct file-based mTLS with the PIV card is not possible — you
  authenticate to the IdP with PIV and present the issued token. `irissync` does
  not run the OIDC flow or refresh tokens (that stays outside the zero-dependency
  binary); it just presents the token you supply. Pulls are fast, so short token
  lifetimes are rarely a problem mid-operation.
- **Service / CI path → mutual TLS.** `--ca-file` (internal CA bundle) +
  `--client-cert`/`--client-key` for a **service or derived (PIV-D) certificate**
  — not the PIV card. PKI-managed renewal replaces ad-hoc password expiry and
  matches the in-boundary TLS posture.
- **Least-privilege identity per environment.** Whichever app auth you use, scope
  it to a dedicated read identity (Atelier app role + read on the routine DB) —
  not `_SYSTEM`, not your own superuser login. A **read-only** identity is all
  `irissync` ever needs, which makes it a natural fit for **pre-prod**.
- **Secrets by file, not argv/env.** Prefer `--token-file` / `--password-file`:
  the secret never appears in a process listing or the environment. App auth
  (token or basic) layers on top of the optional mTLS transport — set both. A
  `401` means app auth failed; a TLS error means the transport/cert is wrong.

**Many instances.** Because config is just flags + env + secret files, point
`irissync` at each system with a per-instance shell profile / wrapper (or, if
present, let `m-cli`'s `.m-cli.toml` resolve `[iris.dev-a]`, `[iris.preprod]`, …
and invoke `irissync`). Keep **non-secret** params (URLs, namespaces, instance
labels, cert/secret *paths*) in version control; keep the secret **files**
themselves out (OS keychain / secret store / mounted path). The mirror is already
`<instance>/<namespace>`-keyed, so every environment's tree and manifest coexist
without collision.

```sh
# one profile per target instance; secrets by file reference, never committed
export IRISSYNC_BASE_URL=https://preprod-host:52773/api/atelier/v1/
export IRISSYNC_INSTANCE=preprod IRISSYNC_NAMESPACE=VISTA
export IRISSYNC_CA_FILE=/etc/va/ca-bundle.pem
# service/CI cert (not a PIV card):
export IRISSYNC_CLIENT_CERT=~/.irissync/preprod.crt IRISSYNC_CLIENT_KEY=~/.irissync/preprod.key
# human SSO token, written to a private file by your token helper:
get-sso-token preprod > ~/.irissync/preprod.token   # mode 0600
export IRISSYNC_TOKEN_FILE=~/.irissync/preprod.token
irissync pull --type int
```

## Mirror layout

```
<mirror>/<instance>/<namespace>/<ROUTINE>.<type>     # e.g. DGREG.int, %ZSTART.mac
<mirror>/<instance>/<namespace>/.irissync-manifest.json
```

The `<mirror>` root defaults to **`.m-cache`** *relative to the current
directory* (`--mirror` / `IRISSYNC_MIRROR` to change it — use an absolute path
for a stable location); `<instance>` and `<namespace>` come from their flags.
Each file is named for the server docname; the `<type>` suffix follows `--type`
(`int` for `^%RI`-loaded VistA, `mac` for ObjectScript — see below).

Writes are **atomic** (temp + rename) and normalize line endings to `\n` so the
tree is git-stable and `tree-sitter-m`-parseable. Source is fetched as **plain
UDL/Atelier text** — the XML `$SYSTEM.OBJ.Export` wrapper is refused; `.cls`
(ObjectScript classes) is out of scope.

> **Layout note:** [design §2.1](https://github.com/vista-cloud-dev/vista-dev-bridge/blob/main/docs/liberation-binary-design.md)
> illustrates an extra `<package>` path segment. Deriving a VistA package from a
> bare routine name needs the package-prefix map (a `vista-meta` domain concern
> the read gate doesn't have), so routines are written **flat** under the
> namespace for now. The manifest (keyed by full docname) is the source of truth
> either way.

## Manifest

`.irissync-manifest.json` makes the mirror an incremental cache (`pull` fetches
only new/changed) and a verifiable artifact (`verify` re-hashes against it; it is
also the conflict-check basis for `push`). One entry per routine:

```json
{
  "schema": 1,
  "instance": "vehu-dev",
  "namespace": "VISTA",
  "pulledAt": "2026-05-27T00:00:00Z",
  "routines": {
    "DGREG.int": { "serverTS": "2026-05-20 09:14:22.000", "sha256": "…", "bytes": 4821 }
  }
}
```

Keys marshal in sorted order, so the file diffs cleanly in git.

### Routine type — `.mac` vs `.int` (VistA)

On a VistA loaded into IRIS via `^%RI`, the routine **source** is stored as
`.int` (classic MUMPS), not `.mac` — `GET doc/DGREG.mac` is a 404 while
`docnames/RTN/int` lists ~34k real routines. The "never pull `.int`" rule in
`liberation-binary-design.md` is correct for ObjectScript (where `.int` is
compiler output) but not for `^%RI`-loaded VistA, where `.int` *is* the source.
Use `--type int` for such instances (default stays `mac`). Validated 2026-05-27
against a live IRIS-for-Health VistA: a full `pull --type int` materialized all
**34,023** routines (140.8 MB) in ~51 s (and a `DG*` subset, 1,484 routines, in
~3.5 s); `status` and `verify` both clean.

### Repair semantics

`pull` is incremental against the manifest. It **self-heals a deleted/partial
mirror file** (re-fetched on the next `pull`), but content **tampering** (file
present, hash differs) is intentionally *not* re-hashed on every pull — `verify`
detects it (exit 3) and `pull --full` repairs it.

## Write-back (`push`) — the sole DB writer

`push` is the **only** verb that writes to IRIS, and it is the **single,
bidirectional owner** of the source boundary: nothing else (not `m-cli`, not the
read verbs) writes routine source. It reads each edited routine from the mirror,
PUTs it back (`PUT …/doc/{name}`), then **compiles-on-import**
(`POST …/action/compile`) to validate the write and regenerate the read-only
`.int`. Because a read-only tool is gaining a write verb, the write is gated by
**three single-writer layers** ([design §5](https://github.com/vista-cloud-dev/vista-dev-bridge/blob/main/docs/liberation-binary-design.md)),
narrowest → widest scope:

1. **Local exclusive lock** — `<mirror>/<instance>/<ns>/.irissync-push.lock`,
   created atomically (`O_CREATE|O_EXCL`), holding `{host, pid, startedAt}`.
   Serializes concurrent `irissync push` against the same mirror/namespace; a
   stale lock (dead PID on this host, or older than `--lock-ttl`) is reclaimed
   with a warning. A live lock → **exit 4** (`LOCK_HELD`).
2. **Manifest conflict-check (the cross-writer guard).** Before each PUT,
   `push` re-reads the routine's live server timestamp and compares it to the
   entry recorded at the last `pull`. If the server copy changed since you
   pulled — i.e. **any** other writer touched it (changed, deleted, or a routine
   that now exists but was never pulled) — that routine is **refused (exit 4)**,
   not clobbered, unless `--force`. This is what makes "single writer" hold
   against writers `irissync` does not control.
3. **Detect-and-defer.** A routine the server marks **non-updatable** (the
   Atelier `upd` flag — e.g. held by the InterSystems ObjectScript extension /
   `%Studio.SourceControl`) is **deferred** (exit 4), not fought over, unless
   `--force`.

The push sequence is: scope the manifest's routines that have a local file →
conflict-check + detect-and-defer (a full plan, also what `--dry-run` prints) →
acquire the lock → for each writable routine `PUT …/doc/{name}` → `POST
…/action/compile` → refresh the manifest entry to the new server timestamp/hash
→ release the lock. A compile failure leaves the source saved but **flagged** —
the write itself succeeded, so this is a *finding* (**exit 3**), not a refusal
(exit 4); the manifest still records the new server state so the next
`status`/`verify` is accurate.

```sh
# Round-trip: pull, edit in the mirror, push back (gate G3).
irissync pull
$EDITOR .m-cache/vehu-dev/VISTA/DGREG.mac
irissync push --dry-run     # plan: to-push / up-to-date / conflicts / deferred
irissync push               # PUT + compile, locked + conflict-checked
irissync verify             # clean — the manifest matches the pushed file
irissync status             # in sync — no drift
```

If someone changed `DGREG.mac` on the server after your `pull`, `push` refuses
with **exit 4** (`PUSH_REFUSED`) and writes nothing — re-`pull` to reconcile, or
`--force` to override. Push needs a pulled mirror (its conflict-check basis); it
errors if there is no manifest.

## Output contract and exit codes

Every command speaks the shared `clikit` contract: `--output`/`-o`
`text|json|auto` (default `auto` — styled text on a TTY, the JSON envelope when
piped), plus `--no-color` and `--verbose`/`-v`, and a deterministic exit-code
ladder.

| Exit | Meaning |
|------|---------|
| `0` | success / in sync |
| `1` | runtime error (auth / TLS / IO) |
| `2` | usage error (missing/invalid flags) |
| `3` | **drift** (`status`), **mismatch** (`verify`), or **compile error** (`push` wrote the source but it did not compile cleanly) — CI gates on this **without parsing output** |
| `4` | **`push` refused** — a conflict (server changed since pull), the lock is held, or a routine is deferred (held by another writer). Nothing was written; re-pull or pass `--force`. |

For `status`/`verify`, the full report is on **stdout** (JSON envelope or text);
on drift/mismatch the process **exits 3** and a concise reason goes to stderr.

## Build

```sh
make build          # dist/irissync — static (CGO_ENABLED=0), -trimpath, version-stamped
make test           # go test -race -cover ./...
make dist           # cross-compile: linux/{amd64,arm64}, darwin/arm64, windows/amd64
make schema         # emit the JSON schema (a CI conformance artifact)
```

Builds are static and `CGO_ENABLED=0` so the binary runs on locked-down VA
hosts, scratch containers, dev macs, and CI alike.

## Architecture

```
main.go ──► Kong grammar (clikit.Globals + config.Conn)
              │
   cmd.Run(cc *clikit.Context, conn *config.Conn)
              │
   internal/config   resolve flags > env; validate; build the client + layout
   internal/atelier  Atelier REST v1 client (net/http + crypto/tls + crypto/x509)
                       docnames → []DocName · GET doc → source · PUT doc · action/compile
   internal/manifest  load/save .irissync-manifest.json · server⇄mirror diff · push conflict-check
   internal/mirror    atomic routine writer (EOL normalize, UDL-only guard) · re-hash
   internal/lock      exclusive push lock (O_CREATE|O_EXCL; PID/host/TTL stale reclaim)
```

## Dependency note (zero-`require` SBOM)

[`liberation-binary-design.md`](https://github.com/vista-cloud-dev/vista-dev-bridge/blob/main/docs/liberation-binary-design.md)
calls for `irissync` to be **zero-`require`** (Go stdlib only) so its SBOM
reduces to "Go stdlib at toolchain version *X*" — *the absence of `require`
lines is the attested artifact.* This repo was instead scaffolded from
`go-cli-template`, so it currently carries the shared `clikit` CLI dependencies
(Kong + Lipgloss + kongplete + x/term) for an identical look-and-feel across the
toolchain. **All IRIS/source logic in `internal/` already uses the stdlib only**
(`net/http`, `crypto/tls`, `crypto/x509`, `crypto/sha256`, `encoding/json`,
`os`), so dropping back to the zero-`require` invariant later is a `clikit`-shaped
change, not a rewrite. Revisit before the FedRAMP-HIGH SBOM step.

## License

**Apache-2.0** — see [`LICENSE`](LICENSE) and [`NOTICE`](NOTICE).
