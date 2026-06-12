package main

import (
	"context"
	"fmt"
	"strings"

	mdriver "github.com/vista-cloud-dev/m-driver-sdk"
	"github.com/vista-cloud-dev/m-iris/clikit"
	"github.com/vista-cloud-dev/m-iris/internal/config"
	"github.com/vista-cloud-dev/m-iris/internal/remote"
)

// execCmd is the exec axis (driver-contract §5.3) over the IRIS `remote`
// transport: run M against the attached namespace through the m.iris.Runner
// substrate. load PUT+compiles routine source over Atelier (neutral .m source is
// staged as a classic .int routine); run executes an entryref and eval one
// command, each through the runner's fault trap that surfaces a structured
// engineError (§7) on a runtime fault. All three ride internal/remote.Transport
// — the substrate the remote spike de-risked; this axis wires it to the CLI so
// the SDK reference Client (and therefore `v pkg install`) can drive a lifecycle.
type execCmd struct {
	Load  execLoadCmd  `cmd:"" name:"load" help:"Stage routine source into the namespace (Atelier PUT) and compile it; neutral .m → .int. Compile faults surface as engineError."`
	Run   execRunCmd   `cmd:"" name:"run" help:"Run an entryref (LABEL^ROUTINE) through the runner; args → the formallist. Faults surface as engineError."`
	Eval  execEvalCmd  `cmd:"" name:"eval" help:"Evaluate a single M command through the runner. Faults surface as engineError."`
	Abort execAbortCmd `cmd:"" name:"abort" help:"Stop a run still in flight under an ephemeral --prefix (the runner terminates its recorded process)."`
}

type execResult struct {
	Stdout string `json:"stdout"`
	Status int    `json:"status"`
}

type execLoadResult struct {
	Loaded   []string `json:"loaded"`
	Compiled bool     `json:"compiled"`
}

// remoteTransport builds the remote (Atelier REST + runner) transport for the
// exec axis, after refusing the not-yet-wired local/docker transports.
func remoteTransport(conn *config.Conn) (*remote.Transport, error) {
	if err := remoteOnly(conn); err != nil {
		return nil, err
	}
	client, err := remoteClient(conn)
	if err != nil {
		return nil, err
	}
	return remote.New(client), nil
}

// --- load --------------------------------------------------------------------

type execLoadCmd struct {
	Paths  []string `arg:"" optional:"" help:"Routine source files (or directories) to stage."`
	Prefix string   `help:"Ephemeral docname prefix applied to each staged routine." placeholder:"PREFIX"`
}

func (c *execLoadCmd) Run(cc *clikit.Context, conn *config.Conn) error {
	if len(c.Paths) == 0 {
		return clikit.Fail(clikit.ExitUsage, "NO_SOURCE", "exec load needs <paths…>", "")
	}
	tr, err := remoteTransport(conn)
	if err != nil {
		return err
	}
	res, err := tr.Load(context.Background(), mdriver.LoadRequest{Paths: c.Paths, Prefix: c.Prefix})
	if err != nil {
		return runtimeErr(err)
	}
	if res.EngineError != nil {
		msg := strings.TrimSpace(res.EngineError.Mnemonic + " " + res.EngineError.Text)
		return clikit.FailEngine(clikit.ExitRuntime, "COMPILE_ERROR", "compile failed: "+msg, "", toClikitEngineError(res.EngineError))
	}
	return cc.Result(execLoadResult{Loaded: nonNil(res.Loaded), Compiled: true}, func() {
		cc.Title("load complete")
		cc.KV([2]string{"loaded", fmt.Sprint(len(res.Loaded))}, [2]string{"compiled", "yes"})
		fmt.Fprintln(cc.Stdout, cc.Success("routines staged + compiled"))
	})
}

// --- run ---------------------------------------------------------------------

type execRunCmd struct {
	EntryRef string   `arg:"" help:"Entryref to run (LABEL^ROUTINE or ^ROUTINE)."`
	Args     []string `arg:"" optional:"" help:"Arguments passed to the entryref."`
	Prefix   string   `help:"Ephemeral-run prefix; the runner keys its result global by it." placeholder:"PREFIX"`
}

func (c *execRunCmd) Run(cc *clikit.Context, conn *config.Conn) error {
	return runExec(cc, conn, mdriver.ExecRequest{EntryRef: c.EntryRef, Args: c.Args, Prefix: c.Prefix})
}

// --- abort -------------------------------------------------------------------

type execAbortCmd struct {
	Prefix string `help:"Ephemeral-run prefix to abort (the run id passed to 'exec run --prefix')." placeholder:"PREFIX"`
}

type execAbortResult struct {
	Killed []string `json:"killed"`
}

func (c *execAbortCmd) Run(cc *clikit.Context, conn *config.Conn) error {
	if c.Prefix == "" {
		return clikit.Fail(clikit.ExitUsage, "NO_PREFIX", "exec abort needs --prefix", "")
	}
	tr, err := remoteTransport(conn)
	if err != nil {
		return err
	}
	killed, err := tr.Abort(context.Background(), c.Prefix)
	if err != nil {
		return runtimeErr(err)
	}
	return cc.Result(execAbortResult{Killed: nonNil(killed)}, func() {
		if len(killed) == 0 {
			fmt.Fprintln(cc.Stdout, cc.Faint("no run in flight under --prefix "+c.Prefix))
			return
		}
		fmt.Fprintln(cc.Stdout, cc.Success(fmt.Sprintf("aborted %d run(s): %s", len(killed), strings.Join(killed, ", "))))
	})
}

// --- eval --------------------------------------------------------------------

type execEvalCmd struct {
	Command []string `arg:"" help:"M command to evaluate (joined with spaces; quote it as one shell arg)."`
}

func (c *execEvalCmd) Run(cc *clikit.Context, conn *config.Conn) error {
	return runExec(cc, conn, mdriver.ExecRequest{Command: strings.Join(c.Command, " ")})
}

// --- shared ------------------------------------------------------------------

// runExec dispatches req through the remote runner and renders the result: a §7
// fault becomes an ok=false envelope with engineError (exit 5); otherwise
// {stdout, status}.
func runExec(cc *clikit.Context, conn *config.Conn, req mdriver.ExecRequest) error {
	tr, err := remoteTransport(conn)
	if err != nil {
		return err
	}
	res, err := tr.Exec(context.Background(), req)
	if err != nil {
		return runtimeErr(err)
	}
	if res.EngineError != nil {
		msg := res.EngineError.Mnemonic
		if res.EngineError.Text != "" {
			msg = strings.TrimSpace(msg + " " + res.EngineError.Text)
		}
		return clikit.FailEngine(clikit.ExitRuntime, "ENGINE_ERROR", msg, "", toClikitEngineError(res.EngineError))
	}
	return cc.Result(execResult{Stdout: res.Stdout, Status: res.Status}, func() {
		if res.Stdout != "" {
			fmt.Fprint(cc.Stdout, res.Stdout)
			if !strings.HasSuffix(res.Stdout, "\n") {
				fmt.Fprintln(cc.Stdout)
			}
		}
		fmt.Fprintln(cc.Stdout, cc.Faint(fmt.Sprintf("status %d", res.Status)))
	})
}

// toClikitEngineError converts the SDK §7 fault to clikit's own copy (drivers
// convert at the envelope boundary — consistency-protocol).
func toClikitEngineError(e *mdriver.EngineError) *clikit.EngineError {
	if e == nil {
		return nil
	}
	return &clikit.EngineError{
		Routine:  e.Routine,
		Line:     e.Line,
		Mnemonic: e.Mnemonic,
		Text:     e.Text,
	}
}
