// Command irissync is the IRIS-specific source-sync binary: the sole owner of
// the IRIS source boundary. This build implements the read side — the P0
// source-axis gate — materializing IRIS routine source into a git-friendly
// .mac mirror and verifying it. Write-back (`push`) lands in stage 2.1.
//
//	irissync list      inventory server docnames (connectivity/auth smoke test)
//	irissync pull      DB → .mac mirror + manifest, incremental
//	irissync status    server vs. local manifest drift (exit 3 on drift)
//	irissync verify    re-hash mirror files against the manifest (exit 3 on mismatch)
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

	Schema  clikit.SchemaCmd  `cmd:"" help:"Emit the command/flag tree as JSON (agent discovery)."`
	Version clikit.VersionCmd `cmd:"" help:"Show version and build info."`

	InstallCompletions kongplete.InstallCompletions `cmd:"" help:"Install shell tab-completions."`
}

func main() {
	cli := &CLI{}
	os.Exit(clikit.Run(
		"irissync",
		"IRIS source-sync — materialize IRIS routine source to a .mac mirror and verify it (read side).",
		cli, &cli.Globals,
		kong.Bind(&cli.Conn),
	))
}
