// Package driver holds the vendor-neutral m engine-driver contract types
// (driver-contract.md v1.0) as m-iris implements them: the capability document,
// the verb-level Transport seam (local/docker/remote), and the structured
// engine-error shape. These are vendored thin locally until the shared
// m-driver-sdk is extracted at the Phase-0 checkpoint (kickoff-prompts.md
// "Coordination model"); m-iris then depends on the SDK and deletes the copies.
//
// Nothing here knows about m-cli — the driver implements vendor logic only,
// against the frozen contract.
package driver

// ContractVersion is the driver-contract major.minor this binary implements and
// advertises in caps. m-cli refuses a driver whose major it does not understand.
const ContractVersion = "1.0"

// Caps is the capability document (driver-contract.md §4). m-cli calls
// `m-iris meta caps` before optional verbs and adapts to exactly what is
// advertised; calling an unadvertised verb yields exit 7 (unsupported).
type Caps struct {
	Engine     string              `json:"engine"`
	Contract   string              `json:"contract"`
	Transports []string            `json:"transports"`
	Axes       map[string][]string `json:"axes"`
	Features   map[string]bool     `json:"features"`
}

// caps is the live document. It is HONEST by construction: it lists only the
// axes/verbs that are actually wired in this build, and grows milestone by
// milestone (M1 lifecycle, M3 exec, M4 data, M5 cover, M6 admin, M7 native).
// Conformance asserts advertised == implemented, so do not list a verb here
// before its command exists.
func capsDoc() Caps {
	return Caps{
		Engine:     "iris",
		Contract:   ContractVersion,
		Transports: []string{"local", "docker", "remote"},
		Axes: map[string][]string{
			// M0 — meta + the existing irissync source verbs, regrouped under sync.
			"meta": {"caps", "version", "info", "schema"},
			"sync": {"list", "pull", "status", "verify", "push", "deploy"},
		},
		Features: map[string]bool{
			"remote":          true,  // IRIS reaches over Atelier REST
			"prune":           true,  // sync deploy --prune true-sync
			"ephemeralPrefix": true,  // exec --prefix zzt<runid> staging
			"snapshot":        false, // lifecycle snapshot/rollback — not yet (roadmap §10)
		},
	}
}

// CapsDoc returns the live capability document. Exported as a function (not a
// var) so the slices/maps cannot be mutated by a caller.
func CapsDoc() Caps { return capsDoc() }
