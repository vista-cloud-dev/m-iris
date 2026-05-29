// Command irissync is the sole bidirectional owner of the IRIS source boundary:
// it materializes the M routines of a namespace into a git-friendly mirror +
// manifest (the read side), and writes edited routines back to IRIS (push).
//
// The read verbs (list/pull/status/verify) are safe by construction — every
// IRIS operation is a GET; the only writes are to the local mirror. `push` is
// the opt-in write path and the SOLE DB WRITER: it is gated by a single-writer
// lock + a manifest conflict-check + detect-and-defer (liberation-binary-design
// §5) so it never clobbers a change made underneath it.
//
//	irissync list      inventory server docnames (connectivity/auth smoke test)
//	irissync pull      DB → .mac mirror + manifest, incremental
//	irissync status    server vs. local manifest drift (exit 3 on drift)
//	irissync verify    re-hash mirror files against the manifest (exit 3 on mismatch)
//	irissync push      write edited routines back to IRIS (PUT + compile), conflict-checked, locked (exit 4 on refusal)
//	irissync version   build + Go toolchain info
//	irissync schema    machine-readable command tree (agent discovery)
//
// Connection config comes from flags or IRISSYNC_* env (flags win); see
// internal/config and liberation-binary-design.md §2/§3.
package main

import (
	"os"

	"github.com/alecthomas/kong"
	"github.com/willabides/kongplete"

	"github.com/vista-cloud-dev/irissync/clikit"
	"github.com/vista-cloud-dev/irissync/internal/config"
)

// CLI is the root command grammar. clikit.Globals (--output/--no-color/-v) and
// config.Conn (connection + behavior flags) are embedded, so both contribute
// global flags; config.Conn is bound so commands receive a *config.Conn.
type CLI struct {
	clikit.Globals
	config.Conn

	List   listCmd   `cmd:"" help:"List server routine docnames (no writes) — connectivity + inventory."`
	Pull   pullCmd   `cmd:"" help:"Materialize IRIS routine source → .mac mirror, incremental via the manifest."`
	Status statusCmd `cmd:"" help:"Diff server vs. local manifest: new / changed / deleted (exit 3 on drift)."`
	Verify verifyCmd `cmd:"" help:"Re-hash mirror files against the manifest (exit 3 on mismatch)."`
	Push   pushCmd   `cmd:"" help:"Write edited routines back to IRIS (PUT + compile) — the sole DB writer; conflict-checked + single-writer-locked (exit 4 on refusal)."`

	Schema  clikit.SchemaCmd  `cmd:"" help:"Emit the command/flag tree as JSON (agent discovery)."`
	Version clikit.VersionCmd `cmd:"" help:"Show version and build info."`

	InstallCompletions kongplete.InstallCompletions `cmd:"" help:"Install shell tab-completions."`
}

func main() {
	cli := &CLI{}
	os.Exit(clikit.Run(
		"irissync",
		"IRIS source-sync — materialize IRIS routine source to a .mac mirror (read), and write edited routines back (push, the sole DB writer).",
		cli, &cli.Globals,
		kong.Bind(&cli.Conn),
	))
}
