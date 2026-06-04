package main

import (
	"context"
	"fmt"
	"time"

	mdriver "github.com/vista-cloud-dev/m-driver-sdk"
	"github.com/vista-cloud-dev/m-iris/clikit"
	"github.com/vista-cloud-dev/m-iris/internal/atelier"
	"github.com/vista-cloud-dev/m-iris/internal/config"
)

// lifecycleCmd is the lifecycle axis (driver-contract §5.1): manage the engine
// instance. On the IRIS `remote` transport the driver ATTACHES to an existing
// namespace and manages routines only — you cannot create or destroy a namespace
// over Atelier (risk B4) — so provision/destroy report unsupported (exit 7) and
// conformance runs in attached mode. up verifies reachability; down/restart are
// no-ops; status/wait drive health off the Atelier root probe. The docker/local
// strategies (container / `iris start`) land with the M3 session transports.
type lifecycleCmd struct {
	Up        lifeUpCmd        `cmd:"" help:"Bring the engine into a usable state (remote: verify reachable + attach)."`
	Down      lifeDownCmd      `cmd:"" help:"Stop the engine (remote: no-op — the server is not ours to stop)."`
	Restart   lifeRestartCmd   `cmd:"" help:"Restart the engine (remote: re-verify reachable)."`
	Status    lifeStatusCmd    `cmd:"" help:"Report running/healthy/version/namespaces; --probe for a terse CI readiness gate."`
	Wait      lifeWaitCmd      `cmd:"" help:"Block until the engine is healthy or --timeout elapses (exit 6 on timeout)."`
	Provision lifeProvisionCmd `cmd:"" help:"Create an instance/namespace (remote: unsupported over Atelier, exit 7)."`
	Destroy   lifeDestroyCmd   `cmd:"" help:"Remove an instance/namespace (remote: unsupported over Atelier, exit 7)."`
}

// The lifecycle status/state payloads are SDK-owned so m-ydb and m-iris emit
// identical JSON m-cli reads (aliases keep the existing literals/renderers).
type (
	lifecycleStatus = mdriver.Status
	lifeStateResult = mdriver.StateResult
)

// remoteOnly returns a not-yet-implemented error for local/docker (only remote
// is wired today) and nil for remote. An empty transport defaults to remote.
func remoteOnly(conn *config.Conn) error {
	switch conn.Transport {
	case "", "remote":
		return nil
	default:
		return clikit.Fail(clikit.ExitRuntime, "TRANSPORT_NOT_IMPLEMENTED",
			fmt.Sprintf("transport %q is not yet wired in m-iris (only remote today)", conn.Transport),
			"use --transport remote, or wait for the M3 local+docker session transports")
	}
}

// remoteClient builds the Atelier client for the remote transport.
func remoteClient(conn *config.Conn) (*atelier.Client, error) {
	if err := conn.Validate(config.Need{Network: true}); err != nil {
		return nil, usageErr(err)
	}
	acfg, err := conn.Atelier()
	if err != nil {
		return nil, usageErr(err)
	}
	c, err := atelier.New(acfg)
	if err != nil {
		return nil, runtimeErr(err)
	}
	return c, nil
}

// probeRemote probes the Atelier root and classifies the result: reachable+ok,
// reachable-but-auth-failed (server answered 401/403), or unreachable.
func probeRemote(ctx context.Context, conn *config.Conn) (lifecycleStatus, error) {
	client, err := remoteClient(conn)
	if err != nil {
		return lifecycleStatus{}, err
	}
	st := lifecycleStatus{Transport: "remote", Endpoint: conn.BaseURL}
	start := time.Now()
	info, err := client.ServerInfo(ctx)
	st.LatencyMs = time.Since(start).Milliseconds()
	switch {
	case err == nil:
		st.Running, st.Healthy = true, true
		st.Version, st.Namespaces = info.Version, info.Namespaces
	case atelier.IsUnauthorized(err), atelier.IsForbidden(err):
		st.Running, st.Healthy = true, false // the server answered; the credential failed
	default:
		st.Running, st.Healthy = false, false
	}
	return st, nil
}

func engineUnreachable(msg string) error {
	return clikit.Fail(clikit.ExitUnreachable, "UNREACHABLE", msg,
		"verify --base-url and credentials; run `m-iris meta doctor`")
}

// --- lifecycle status / --probe ---------------------------------------------

type lifeStatusCmd struct {
	Probe bool `help:"Terse readiness gate: {running, healthy, latencyMs}; exit 0 healthy, 6 not ready."`
}

func (c lifeStatusCmd) Run(cc *clikit.Context, conn *config.Conn) error {
	if err := remoteOnly(conn); err != nil {
		return err
	}
	st, err := probeRemote(context.Background(), conn)
	if err != nil {
		return err
	}
	if c.Probe {
		terse := lifecycleStatus{Transport: st.Transport, Running: st.Running, Healthy: st.Healthy, LatencyMs: st.LatencyMs}
		if rerr := cc.Result(terse, func() {
			cc.KV([2]string{"healthy", fmt.Sprint(terse.Healthy)}, [2]string{"latencyMs", fmt.Sprint(terse.LatencyMs)})
		}); rerr != nil {
			return rerr
		}
		if !st.Healthy {
			return clikit.Fail(clikit.ExitUnreachable, "NOT_READY", "engine not ready", "run `m-iris meta doctor` for the cause")
		}
		return nil
	}
	return cc.Result(st, func() {
		cc.Title("engine status — " + st.Transport)
		cc.KV(
			[2]string{"running", fmt.Sprint(st.Running)},
			[2]string{"healthy", fmt.Sprint(st.Healthy)},
			[2]string{"version", st.Version},
			[2]string{"namespaces", fmt.Sprint(st.Namespaces)},
		)
	})
}

// --- lifecycle up / down / restart ------------------------------------------

type lifeUpCmd struct{}

func (lifeUpCmd) Run(cc *clikit.Context, conn *config.Conn) error {
	if err := remoteOnly(conn); err != nil {
		return err
	}
	st, err := probeRemote(context.Background(), conn)
	if err != nil {
		return err
	}
	if !st.Running {
		return engineUnreachable("up: engine is not reachable to attach to")
	}
	return cc.Result(lifeStateResult{State: "attached", Endpoint: conn.BaseURL}, func() {
		fmt.Fprintln(cc.Stdout, cc.Success("attached to "+conn.BaseURL))
	})
}

type lifeDownCmd struct{}

func (lifeDownCmd) Run(cc *clikit.Context, conn *config.Conn) error {
	if err := remoteOnly(conn); err != nil {
		return err
	}
	// The remote server is not ours to stop; down is a no-op that just detaches.
	return cc.Result(lifeStateResult{State: "detached"}, func() {
		fmt.Fprintln(cc.Stdout, "detached (remote engine left running)")
	})
}

type lifeRestartCmd struct{}

func (lifeRestartCmd) Run(cc *clikit.Context, conn *config.Conn) error {
	return (lifeUpCmd{}).Run(cc, conn)
}

// --- lifecycle wait ----------------------------------------------------------

type lifeWaitCmd struct {
	Timeout int `default:"60" help:"Seconds to wait for readiness before giving up (exit 6)."`
}

func (c *lifeWaitCmd) Run(cc *clikit.Context, conn *config.Conn) error {
	if err := remoteOnly(conn); err != nil {
		return err
	}
	deadline := time.Now().Add(time.Duration(c.Timeout) * time.Second)
	const poll = 100 * time.Millisecond
	var st lifecycleStatus
	for {
		var err error
		st, err = probeRemote(context.Background(), conn)
		if err != nil {
			return err
		}
		if st.Healthy {
			return cc.Result(st, func() {
				fmt.Fprintln(cc.Stdout, cc.Success(fmt.Sprintf("healthy in %dms", st.LatencyMs)))
			})
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(poll)
	}
	_ = cc.Result(st, nil)
	return clikit.Fail(clikit.ExitUnreachable, "WAIT_TIMEOUT",
		fmt.Sprintf("engine not healthy after %ds", c.Timeout), "check the engine is up and reachable")
}

// --- lifecycle provision / destroy (unsupported on remote) ------------------

type lifeProvisionCmd struct{}

func (lifeProvisionCmd) Run(cc *clikit.Context, conn *config.Conn) error {
	return unsupportedOnRemote(conn, "provision",
		"create the namespace on the server, then attach with --transport remote")
}

type lifeDestroyCmd struct{}

func (lifeDestroyCmd) Run(cc *clikit.Context, conn *config.Conn) error {
	return unsupportedOnRemote(conn, "destroy",
		"drop the namespace on the server directly; m-iris remote only manages routines")
}

// unsupportedOnRemote reports exit 7 for provision/destroy on remote (a namespace
// cannot be created or removed over Atelier — risk B4) and not-implemented for
// local/docker until those transports land.
func unsupportedOnRemote(conn *config.Conn, verb, hint string) error {
	switch conn.Transport {
	case "", "remote":
		return clikit.Fail(clikit.ExitUnsupported, "UNSUPPORTED_ON_REMOTE",
			verb+" is impossible over Atelier — remote attaches to an existing namespace", hint)
	default:
		return remoteOnly(conn)
	}
}
