// Package irisdriver is m-iris's public, importable surface: it constructs an
// IRIS mdriver.Transport for in-process consumers (notably m-cli's VistaEngine)
// that speak only the neutral engine-driver contract. All vendor logic stays in
// internal/ — this package is the thin seam that lets another module hold an
// IRIS Transport without reaching into m-iris's internals.
//
// The transport is the `remote` substrate: every ObjectScript operation rides a
// role-gated runner class over the Atelier REST API (Atelier has no raw "run"
// endpoint), deployed on first use. This is how VistaEngine reaches a
// routines-embedded IRIS VistA over the network — the symmetric peer of the
// YottaDB SSH transport, both behind one mdriver.Transport.
package irisdriver

import (
	mdriver "github.com/vista-cloud-dev/m-driver-sdk"
	"github.com/vista-cloud-dev/m-iris/internal/atelier"
	"github.com/vista-cloud-dev/m-iris/internal/remote"
)

// Config is the IRIS Atelier connection. It re-exports the internal client
// config so external callers configure the engine without importing internal/:
// BaseURL (…/api/atelier/v1/), Namespace, and auth (Token | User+Password,
// optional CAFile / ClientCert / ClientKey for in-boundary or mutual TLS).
type Config = atelier.Config

// New builds an IRIS Transport over the Atelier REST API. Construction does not
// dial the server; the runner class is PUT+compiled lazily on the first verb.
// Callers hold the result as an mdriver.Transport; the T0.1 readiness gate is
// Health (a privileged SELECT 1), and `W $ZV` is Exec of a command.
func New(cfg Config) (mdriver.Transport, error) {
	client, err := atelier.New(cfg)
	if err != nil {
		return nil, err
	}
	return remote.New(client), nil
}
