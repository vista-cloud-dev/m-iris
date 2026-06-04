// Command m-iris is the InterSystems IRIS engine driver for the `m` toolchain:
// a vendor adapter exposing the neutral m engine-driver contract (driver-
// contract.md v1.0) over IRIS, plus the complete native IRIS surface for power
// users. It is the rename + extension of the original `irissync` (whose Atelier
// source axis became the `sync` axis here).
//
// The contract surface is grouped into axes — m-cli speaks only these:
//
//	m-iris meta caps      capability document (axes/transports/features)
//	m-iris meta info      driver identity + resolved engine target
//	m-iris meta version   build + Go toolchain info
//	m-iris meta schema    machine-readable command tree (agent discovery)
//	m-iris sync list      inventory server docnames (connectivity + inventory)
//	m-iris sync pull      DB → mirror + manifest, incremental
//	m-iris sync status    server vs. local manifest drift (exit 3 on drift)
//	m-iris sync verify    re-hash mirror files against the manifest (exit 3)
//	m-iris sync push      write edited routines back to IRIS (the sole DB writer)
//	m-iris sync deploy    install a routine-source library (--prune true-sync)
//
// Later milestones add the lifecycle, exec, data, cover, admin, and native
// axes; caps grows to advertise each as it lands (caps is honest by
// construction — advertised == implemented).
//
// Connection config comes from flags or M_IRIS_* env (flags win); see
// internal/config. Transports: local | docker | remote (Atelier REST).
package main

import (
	"os"

	"github.com/alecthomas/kong"
	"github.com/willabides/kongplete"

	"github.com/vista-cloud-dev/m-iris/clikit"
	"github.com/vista-cloud-dev/m-iris/internal/config"
)

// CLI is the root command grammar. clikit.Globals (--output/--no-color/-v) and
// config.Conn (connection + behavior flags) are embedded, so both contribute
// global flags; config.Conn is bound so commands receive a *config.Conn. The
// contract verbs are grouped into axis subcommands (meta, sync, …).
type CLI struct {
	clikit.Globals
	config.Conn

	Meta metaCmd `cmd:"" help:"Introspection + power tools: caps / info / version / schema."`
	Sync syncCmd `cmd:"" help:"Source axis: routine source ↔ instance (list / pull / status / verify / push / deploy)."`

	InstallCompletions kongplete.InstallCompletions `cmd:"" help:"Install shell tab-completions."`
}

// syncCmd is the sync axis (driver-contract §5.2) — the original irissync source
// verbs, regrouped. The read verbs (list/pull/status/verify) are safe by
// construction (every IRIS operation is a GET; writes go only to the local
// mirror); push is the opt-in write path and the sole DB writer (locked +
// conflict-checked); deploy installs a routine-source library. M2 adds diff/rm
// and the bare-name --filter.
type syncCmd struct {
	List   listCmd   `cmd:"" help:"List server routine docnames (no writes) — connectivity + inventory."`
	Pull   pullCmd   `cmd:"" help:"Materialize IRIS routine source → mirror, incremental via the manifest."`
	Status statusCmd `cmd:"" help:"Diff server vs. local manifest: new / changed / deleted (exit 3 on drift)."`
	Verify verifyCmd `cmd:"" help:"Re-hash mirror files against the manifest (exit 3 on mismatch)."`
	Push   pushCmd   `cmd:"" help:"Write edited routines back to IRIS (PUT + compile) — the sole DB writer; conflict-checked + single-writer-locked (exit 4 on refusal)."`
	Deploy deployCmd `cmd:"" help:"Install a routine-source library (e.g. m-stdlib/src) into a namespace over Atelier (PUT + compile); --prune for a true sync."`
}

func main() {
	cli := &CLI{}
	os.Exit(clikit.Run(
		"m-iris",
		"InterSystems IRIS engine driver for the m toolchain — neutral contract verbs (meta, sync, …) over IRIS, plus the native IRIS surface.",
		cli, &cli.Globals,
		kong.Bind(&cli.Conn),
	))
}
