// Package driver holds the m-iris vendor logic against the neutral engine-driver
// contract: the capability document and the IRIS-specific realization of the
// shared verb-level Transport. The contract shapes and the Transport interface
// live in the shared SDK (github.com/vista-cloud-dev/m-driver-sdk); this package
// knows nothing of m-cli.
package driver

import mdriver "github.com/vista-cloud-dev/m-driver-sdk"

// Caps is the capability document (driver-contract.md §4). m-cli calls
// `m-iris meta caps` before optional verbs and adapts to exactly what is
// advertised; calling an unadvertised verb yields exit 7 (unsupported).
//
// It is HONEST by construction: it lists only the axes/verbs actually wired in
// this build, and grows milestone by milestone (M1 lifecycle, M3 exec, M4 data,
// M5 cover, M6 admin, M7 native). Conformance asserts advertised == implemented,
// so do not list a verb here before its command exists.
func CapsDoc() mdriver.Caps {
	return mdriver.Caps{
		Engine:     "iris",
		Contract:   mdriver.ContractVersion,
		Transports: []string{mdriver.TransportLocal, mdriver.TransportDocker, mdriver.TransportRemote},
		Axes: mdriver.Axes{
			// M0 — meta + the existing irissync source verbs, regrouped under sync.
			Meta: []string{"caps", "version", "info", "schema", "doctor"},
			Sync: []string{"list", "pull", "status", "verify", "push", "deploy", "diff", "rm"},
			// M1 — lifecycle + health probes. provision/destroy are advertised but
			// report unsupported (exit 7) on the remote transport (risk B4).
			Lifecycle: []string{"up", "down", "restart", "status", "wait", "provision", "destroy"},
			// M3 — exec over the remote runner substrate. abort is not wired (the
			// runner has no long-running job model on Atelier yet), so it stays off
			// caps until implemented (honest-by-construction).
			Exec: []string{"load", "run", "eval"},
		},
		Features: mdriver.Features{
			Remote:          true,  // IRIS reaches over Atelier REST
			Prune:           true,  // sync deploy --prune true-sync
			EphemeralPrefix: true,  // exec --prefix zzt<runid> staging
			Snapshot:        false, // lifecycle snapshot/rollback — not yet (roadmap §10)
		},
	}
}
