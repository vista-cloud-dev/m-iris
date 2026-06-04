package main

import (
	"fmt"

	"github.com/vista-cloud-dev/m-iris/clikit"
	"github.com/vista-cloud-dev/m-iris/internal/config"
	"github.com/vista-cloud-dev/m-iris/internal/driver"
)

// metaCmd is the meta axis (driver-contract §5.7): introspection + power tools.
// caps/version/info/schema are wired now; doctor (M1), selftest (M8), native +
// shell (M7) join as their milestones land — and caps grows to advertise them.
type metaCmd struct {
	Caps    capsCmd           `cmd:"" help:"Emit the capability document (axes, transports, features) m-cli reads before calling optional verbs."`
	Info    infoCmd           `cmd:"" help:"Driver identity + resolved engine target (edition/version filled by the M1 probe)."`
	Version clikit.VersionCmd `cmd:"" help:"Show version and build info."`
	Schema  clikit.SchemaCmd  `cmd:"" help:"Emit the command/flag tree as JSON (agent discovery)."`
}

// --- meta caps ---------------------------------------------------------------

type capsCmd struct{}

// Run emits the live capability document. It needs no connection — caps is a
// pure description of what this binary can do.
func (capsCmd) Run(cc *clikit.Context) error {
	caps := driver.CapsDoc()
	return cc.Result(caps, func() {
		cc.Title(fmt.Sprintf("m-iris — IRIS driver (contract %s)", caps.Contract))
		cc.KV(
			[2]string{"engine", caps.Engine},
			[2]string{"transports", fmt.Sprint(caps.Transports)},
		)
		for _, axis := range []string{"lifecycle", "sync", "exec", "data", "cover", "admin", "meta"} {
			if verbs, ok := caps.Axes[axis]; ok {
				cc.Rule(axis)
				fmt.Fprintln(cc.Stdout, "  "+fmt.Sprint(verbs))
			}
		}
	})
}

// --- meta info ---------------------------------------------------------------

type infoCmd struct{}

// infoResult is the driver identity + the resolved engine target. Engine
// edition/version/namespaces come from a live probe (M1 lifecycle); until a
// transport is attached, info reports the static, no-engine facts so it is
// always safe to call (the first thing scaffolding runs).
type infoResult struct {
	Driver    string `json:"driver"`
	Engine    string `json:"engine"`
	Contract  string `json:"contract"`
	Build     string `json:"build"`
	BaseURL   string `json:"baseURL,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

func (infoCmd) Run(cc *clikit.Context, conn *config.Conn) error {
	res := infoResult{
		Driver:    "m-iris",
		Engine:    "iris",
		Contract:  driver.ContractVersion,
		Build:     clikit.Version,
		BaseURL:   conn.BaseURL,
		Namespace: conn.Namespace,
	}
	return cc.Result(res, func() {
		cc.Title("m-iris — driver info")
		cc.KV(
			[2]string{"driver", res.Driver},
			[2]string{"engine", res.Engine},
			[2]string{"contract", res.Contract},
			[2]string{"build", res.Build},
			[2]string{"namespace", res.Namespace},
		)
	})
}
