package driver

import (
	"context"

	"github.com/vista-cloud-dev/m-iris/clikit"
)

// Transport is the verb-level seam every IRIS transport — local, docker,
// remote — implements. It is deliberately NOT a low-level run(argv) (risk B1):
//
//   - local/docker exec pipes ObjectScript to `iris session -U NS` (stdin →
//     stdout) and compiles via $SYSTEM.OBJ.Load;
//   - remote exec is Atelier PUT + action/compile + a SQL action/query into a
//     role-gated runner class — there is NO raw "run ObjectScript" endpoint, no
//     stdout; results come back through a result global the transport reads.
//
// A single argv seam cannot model both shapes, so the contract is verb-level:
// each transport implements its own strategy and the rest of the driver is
// transport-agnostic. This is the interface m-iris contributes to the shared
// m-driver-sdk at the Phase-0 checkpoint (it must also fit m-ydb's session-pipe).
type Transport interface {
	// Health is the readiness/liveness probe behind `lifecycle status --probe`
	// and `wait`. remote: GET /api/atelier/v1/ → 200 + version.
	Health(ctx context.Context) (Health, error)

	// Load stages routine source and compiles it (exec load). local/docker:
	// $SYSTEM.OBJ.Load(path,"ck"); remote: Atelier PUT + action/compile.
	Load(ctx context.Context, req LoadRequest) (LoadResult, error)

	// Exec runs an entryref (with args) or evaluates a single M command (exec
	// run / eval). On a compile/runtime fault it returns ok via the result's
	// EngineError, not a Go error — the fault is data (driver-contract §7).
	Exec(ctx context.Context, req ExecRequest) (ExecResult, error)

	// ReadGlobal reads a global node (or subtree, per Depth) — data get/query
	// and the result-global reads that back exec/cover orchestration.
	ReadGlobal(ctx context.Context, req GlobalRef) (GlobalNode, error)

	// SetGlobal sets a single global node (data set), used to seed fixtures.
	SetGlobal(ctx context.Context, ref, value string) error
}

// Health is the probe result (driver-contract §3 health probes).
type Health struct {
	Running   bool   `json:"running"`
	Healthy   bool   `json:"healthy"`
	Version   string `json:"version,omitempty"`
	LatencyMs int64  `json:"latencyMs"`
}

// LoadRequest stages source for exec. Paths are files or a directory of
// routine source; Prefix (e.g. zzt<runid>) namespaces an ephemeral run so
// teardown is scoped to that prefix.
type LoadRequest struct {
	Paths  []string
	Prefix string
}

// LoadResult reports what was staged + compiled.
type LoadResult struct {
	Loaded []string `json:"loaded"`
}

// ExecRequest runs an entryref or evaluates a command. EntryRef and Command are
// mutually exclusive (run vs eval).
type ExecRequest struct {
	EntryRef string   // e.g. RUN^STDHARN (run)
	Args     []string // positional args → $ZCMDLINE / the entry's formallist
	Command  string   // a single M command (eval)
	Prefix   string   // ephemeral-run prefix
}

// ExecResult is the unified outcome. Stdout is the captured device output
// (local/docker) or the runner's result-global text (remote). EngineError, when
// non-nil, is the §7 structured fault — the transport sets it instead of
// returning a Go error so the caller can render a RED-with-cause envelope.
type ExecResult struct {
	Stdout      string              `json:"stdout"`
	Status      int                 `json:"status"`
	EngineError *clikit.EngineError `json:"engineError,omitempty"`
}

// GlobalRef addresses a global for a read. Order/Depth shape a subtree query
// (data query); empty means a single-node get.
type GlobalRef struct {
	Ref   string
	Order string // "forward" | "reverse"
	Depth int    // 0 = this node only
}

// GlobalNode is a global value, with children for a subtree read.
type GlobalNode struct {
	Ref   string       `json:"ref"`
	Value string       `json:"value,omitempty"`
	Nodes []GlobalNode `json:"nodes,omitempty"`
}
