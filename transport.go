package main

import (
	"context"
	"fmt"

	mdriver "github.com/vista-cloud-dev/m-driver-sdk"
	"github.com/vista-cloud-dev/m-iris/clikit"
	"github.com/vista-cloud-dev/m-iris/internal/config"
	"github.com/vista-cloud-dev/m-iris/internal/remote"
	"github.com/vista-cloud-dev/m-iris/internal/session"
)

// engineTransport is the neutral verb-level Transport plus the driver-local verbs
// that are not on the SDK seam (exec.abort, data.kill, data.query) — both the
// remote and session strategies satisfy it, so the exec + data axes are written
// transport-agnostically against this interface.
type engineTransport interface {
	mdriver.Transport
	Abort(ctx context.Context, prefix string) ([]string, error)
	KillGlobal(ctx context.Context, ref string) error
	QueryGlobal(ctx context.Context, ref, order string, depth int) ([]mdriver.GlobalNode, error)
}

// newExecTransport selects the transport strategy for the resolved connection:
// the remote (Atelier REST + runner) transport, or the local/docker `iris
// session` transport. It validates the inputs each strategy needs.
func newExecTransport(conn *config.Conn) (engineTransport, error) {
	switch conn.Transport {
	case "", mdriver.TransportRemote:
		client, err := remoteClient(conn)
		if err != nil {
			return nil, err
		}
		return remote.New(client), nil
	case mdriver.TransportDocker, mdriver.TransportLocal:
		if err := validateSession(conn); err != nil {
			return nil, err
		}
		return session.New(conn.Session(), nil), nil
	default:
		return nil, clikit.Fail(clikit.ExitUsage, "BAD_TRANSPORT",
			fmt.Sprintf("unknown transport %q", conn.Transport), "use local | docker | remote")
	}
}

// validateSession checks the inputs the local/docker session transport needs: a
// namespace always, and a container name for docker.
func validateSession(conn *config.Conn) error {
	if conn.Namespace == "" {
		return clikit.Fail(clikit.ExitUsage, "NO_NAMESPACE",
			"the local/docker transport needs --namespace (the IRIS namespace to run in)", "")
	}
	if conn.Transport == mdriver.TransportDocker && conn.Container == "" {
		return clikit.Fail(clikit.ExitUsage, "NO_CONTAINER",
			"the docker transport needs --container (or M_IRIS_CONTAINER)", "")
	}
	return nil
}
