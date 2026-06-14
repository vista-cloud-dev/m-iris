package main

import (
	"context"
	"fmt"

	mdriver "github.com/vista-cloud-dev/m-driver-sdk"
	"github.com/vista-cloud-dev/m-iris/clikit"
	"github.com/vista-cloud-dev/m-iris/internal/config"
)

// dataCmd is the data axis (driver-contract §5.4): globals for fixtures, seeding,
// and namespace inspection without dropping into a native session. get/set/kill
// address one node (set/kill accept a subtree); query walks the subtree rooted at
// a reference. Every verb rides engineTransport, so it works on remote (the
// role-gated runner SqlProcs) and on local/docker (`iris session`, indirect
// @ref). export/import (bulk %GO/%GI dumps) land in a follow-up slice.
type dataCmd struct {
	Get   dataGetCmd   `cmd:"" name:"get" help:"Read one global node: {value}."`
	Set   dataSetCmd   `cmd:"" name:"set" help:"Set one global node: {ok}."`
	Kill  dataKillCmd  `cmd:"" name:"kill" help:"Kill a global node or subtree: {ok}."`
	Query dataQueryCmd `cmd:"" name:"query" help:"Walk the subtree rooted at a reference: {nodes[]}."`
}

type dataGetResult struct {
	Value string `json:"value"`
}

type dataOKResult struct {
	OK bool `json:"ok"`
}

type dataQueryResult struct {
	Nodes []mdriver.GlobalNode `json:"nodes"`
}

// --- get ---------------------------------------------------------------------

type dataGetCmd struct {
	Ref string `arg:"" help:"Global reference, e.g. ^DD(0) or ^XUSEC(\"name\")."`
}

func (c *dataGetCmd) Run(cc *clikit.Context, conn *config.Conn) error {
	tr, err := newExecTransport(conn)
	if err != nil {
		return err
	}
	node, err := tr.ReadGlobal(context.Background(), mdriver.GlobalRef{Ref: c.Ref})
	if err != nil {
		return runtimeErr(err)
	}
	return cc.Result(dataGetResult{Value: node.Value}, func() {
		fmt.Fprintln(cc.Stdout, node.Value)
	})
}

// --- set ---------------------------------------------------------------------

type dataSetCmd struct {
	Ref   string `arg:"" help:"Global reference to set."`
	Value string `arg:"" help:"Value to store."`
}

func (c *dataSetCmd) Run(cc *clikit.Context, conn *config.Conn) error {
	tr, err := newExecTransport(conn)
	if err != nil {
		return err
	}
	if err := tr.SetGlobal(context.Background(), c.Ref, c.Value); err != nil {
		return runtimeErr(err)
	}
	return cc.Result(dataOKResult{OK: true}, func() {
		fmt.Fprintln(cc.Stdout, cc.Success("set "+c.Ref))
	})
}

// --- kill --------------------------------------------------------------------

type dataKillCmd struct {
	Ref string `arg:"" help:"Global reference (node or subtree) to kill."`
}

func (c *dataKillCmd) Run(cc *clikit.Context, conn *config.Conn) error {
	tr, err := newExecTransport(conn)
	if err != nil {
		return err
	}
	if err := tr.KillGlobal(context.Background(), c.Ref); err != nil {
		return runtimeErr(err)
	}
	return cc.Result(dataOKResult{OK: true}, func() {
		fmt.Fprintln(cc.Stdout, cc.Success("killed "+c.Ref))
	})
}

// --- query -------------------------------------------------------------------

type dataQueryCmd struct {
	Ref   string `arg:"" help:"Root global reference to walk."`
	Order string `default:"forward" enum:"forward,reverse" help:"Collation order to walk: forward | reverse."`
	Depth int    `default:"0" help:"Max subscript levels below the root to include (0 = the whole subtree)."`
}

func (c *dataQueryCmd) Run(cc *clikit.Context, conn *config.Conn) error {
	tr, err := newExecTransport(conn)
	if err != nil {
		return err
	}
	nodes, err := tr.QueryGlobal(context.Background(), c.Ref, c.Order, c.Depth)
	if err != nil {
		return runtimeErr(err)
	}
	if nodes == nil {
		nodes = []mdriver.GlobalNode{}
	}
	return cc.Result(dataQueryResult{Nodes: nodes}, func() {
		cc.Title(fmt.Sprintf("%d node(s) under %s", len(nodes), c.Ref))
		for _, n := range nodes {
			fmt.Fprintln(cc.Stdout, n.Ref+" = "+n.Value)
		}
	})
}
