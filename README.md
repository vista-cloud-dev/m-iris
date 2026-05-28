# irissync

**A standalone, read-only tool that liberates IRIS routine source to the
filesystem.** `irissync` materializes the M routines of an IRIS namespace into a
git-friendly mirror tree + a verifiable manifest, and tells you when the mirror
has drifted. It is **safe by construction: it never writes to IRIS** — every
operation is a read (`GET`) over the Atelier REST API; the only thing it writes
is the local mirror.

It is a **self-contained binary** — configured entirely by flags + `IRISSYNC_*`
env (secrets optionally from files), with no dependency on the wider `m-cli`
suite. File-based tooling then consumes the mirror as ordinary files.

> **Scope:** this is the **read / liberation** half — `list`, `pull`, `status`,
> `verify`. Write-back (`push`) is intentionally **not** part of this tool today;
> it is a separate, future component (design:
> [`liberation-binary-design.md`](../vista-dev-bridge/docs/liberation-binary-design.md);
> tracked as stage 2.1 of the
> [m-cli Go toolchain plan](../vista-dev-bridge/docs/m-cli-go-toolchain-implementation-plan.md)).
> Keeping this binary read-only is the point: it's the "safe" tool you can run
> against dev/test/pre-prod systems with zero risk to the source.

```sh
export IRISSYNC_BASE_URL=https://host:52773/api/atelier/v1/
export IRISSYNC_NAMESPACE=VISTA
export IRISSYNC_INSTANCE=vehu-dev
irissync list                 # connectivity + inventory (no writes)
irissync pull                 # DB → .mac mirror + manifest (incremental)
irissync status               # server vs. local manifest drift (exit 3 on drift)
irissync verify               # re-hash the mirror against the manifest
```

---

## Commands

| Command | What it does | Writes? |
|---------|--------------|:-------:|
| `list` | Print server routine docnames. Connectivity/auth smoke test + inventory. | no |
| `pull` | Materialize IRIS routine source → `.mac` mirror, incremental via the manifest. | yes |
| `status` | Diff server vs. local manifest: `new` / `changed` / `deleted` / `unchanged`. | no |
| `verify` | Re-hash mirror files against the manifest. Integrity gate for CI. | no |
| `version` | Print version + Go toolchain (the pinned/mirrored audit trail). | no |
| `schema` | Emit the command/flag tree as JSON (agent discovery). | no |

## Configuration — flags and env

Config comes from **flags or `IRISSYNC_*` env** (flags win); `irissync` does not
read `.m-cli.toml` (that stays `m-cli`'s job, which passes resolved values down —
[design §4](../vista-dev-bridge/docs/liberation-binary-design.md)).

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

`list` needs `--base-url` + `--namespace`; `verify` needs `--instance` +
`--namespace`; `pull`/`status` need all three.

## Enterprise & multi-instance auth

`irissync` is a **standalone, portable binary** — it round-trips routines out of
an IRIS system on its own, configured entirely by flags + `IRISSYNC_*` env (with
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
  not `_SYSTEM`, not your own superuser login. On **pre-prod**, scope it
  **read-only** (`pull` from pre-prod; `push` only to dev).
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

> **Layout note:** [design §2.1](../vista-dev-bridge/docs/liberation-binary-design.md)
> illustrates an extra `<package>` path segment. Deriving a VistA package from a
> bare routine name needs the package-prefix map (a `vista-meta` domain concern
> the read gate doesn't have), so routines are written **flat** under the
> namespace for now. The manifest (keyed by full docname) is the source of truth
> either way.

## Manifest

`.irissync-manifest.json` makes the mirror an incremental cache (`pull` fetches
only new/changed) and a verifiable artifact (`verify` re-hashes against it; it is
also the conflict-check basis for the future `push`). One entry per routine:

```json
{
  "schema": 1,
  "instance": "vehu-dev",
  "namespace": "VISTA",
  "pulledAt": "2026-05-27T00:00:00Z",
  "routines": {
    "DGREG.mac": { "serverTS": "2026-05-20 09:14:22.000", "sha256": "…", "bytes": 4821 }
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
against a live IRIS-for-Health VistA: `pull --type int --filter 'DG*'`
materialized 1,484 routines (6.2 MB) in ~3.5 s; `status`/`verify` clean.

### Repair semantics

`pull` is incremental against the manifest. It **self-heals a deleted/partial
mirror file** (re-fetched on the next `pull`), but content **tampering** (file
present, hash differs) is intentionally *not* re-hashed on every pull — `verify`
detects it (exit 3) and `pull --full` repairs it.

## Output contract and exit codes

Every command speaks the shared `clikit` contract: `--output text|json|auto`
(styled text on a TTY, the JSON envelope when piped) and a deterministic
exit-code ladder.

| Exit | Meaning |
|------|---------|
| `0` | success / in sync |
| `1` | runtime error (auth / TLS / IO) |
| `2` | usage error (missing/invalid flags) |
| `3` | **drift** (`status`) or **mismatch** (`verify`) — CI gates on this **without parsing output** |
| `4` | reserved for `push` refusals (conflict / lock / detect-and-defer — stage 2.1) |

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
                       docnames → []DocName · doc → source line array
   internal/manifest  load/save .irissync-manifest.json · server⇄mirror diff
   internal/mirror    atomic .mac writer (EOL normalize, UDL-only guard) · re-hash
```

## Dependency note (zero-`require` SBOM)

[`liberation-binary-design.md`](../vista-dev-bridge/docs/liberation-binary-design.md)
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
