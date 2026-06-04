// Package remote is the IRIS `remote` transport: vendor logic that drives an
// IRIS namespace entirely over the Atelier REST API. Because Atelier has no raw
// "run ObjectScript" endpoint, every ObjectScript operation rides the
// m_iris.Runner class (runner/m_iris.Runner.cls): the transport PUT+compiles it
// once, then invokes its SQL-projected procedures via action/query and reads
// results back out of a result global. This is the entire remote substrate
// (driver-plan §5 task 8, risk B2); remote exec/data/cover/admin all sit on it.
package remote

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	mdriver "github.com/vista-cloud-dev/m-driver-sdk"
	"github.com/vista-cloud-dev/m-iris/internal/atelier"
)

//go:embed runner/m_iris.Runner.cls
var runnerSource string

// runnerDoc is the Atelier docname of the runner class.
const runnerDoc = "m_iris.Runner.cls"

// AtelierAPI is the slice of the Atelier client the remote transport needs. It
// is narrowed to an interface so unit tests inject a fake (recording PUT/Compile
// and scripting Query rows) without an HTTP server — the real *atelier.Client is
// the gated integration tier.
type AtelierAPI interface {
	PutDoc(ctx context.Context, name string, content []string) (*atelier.PutResult, error)
	Compile(ctx context.Context, names []string, flags string) (*atelier.CompileResult, error)
	Query(ctx context.Context, sql string, params ...string) ([]map[string]string, error)
}

// Transport is the remote (Atelier REST + SQL runner) strategy. It satisfies
// mdriver.Transport so the rest of m-iris is transport-agnostic.
type Transport struct {
	api      AtelierAPI
	deployed bool // runner PUT+compiled this process
}

var _ mdriver.Transport = (*Transport)(nil)

// New builds a remote transport over an Atelier client.
func New(api AtelierAPI) *Transport { return &Transport{api: api} }

// ensureRunner PUT+compiles the runner class once. It is idempotent: a fresh
// instance compiles it; subsequent calls are a no-op. (Lazy so a transport that
// only ever reads source — sync — never deploys the runner.)
func (t *Transport) ensureRunner(ctx context.Context) error {
	if t.deployed {
		return nil
	}
	lines := strings.Split(strings.TrimRight(runnerSource, "\n"), "\n")
	if _, err := t.api.PutDoc(ctx, runnerDoc, lines); err != nil {
		return fmt.Errorf("remote: deploy runner: %w", err)
	}
	res, err := t.api.Compile(ctx, []string{runnerDoc}, "cuk")
	if err != nil {
		return fmt.Errorf("remote: compile runner: %w", err)
	}
	if res != nil && !res.OK() {
		return fmt.Errorf("remote: runner did not compile: %s", strings.Join(res.Diagnostics, "; "))
	}
	t.deployed = true
	return nil
}

// runID derives the result-global key for a request; the ephemeral --prefix is
// the natural run id, falling back to a fixed key for one-shot calls.
func runID(prefix string) string {
	if prefix != "" {
		return prefix
	}
	return "m"
}

// Exec runs an entryref or evaluates a command through the runner. A compile/
// runtime fault is data, not a Go error: the runner records it in the result
// global and Exec returns it as ExecResult.EngineError (contract §7).
func (t *Transport) Exec(ctx context.Context, req mdriver.ExecRequest) (mdriver.ExecResult, error) {
	if err := t.ensureRunner(ctx); err != nil {
		return mdriver.ExecResult{}, err
	}
	rid := runID(req.Prefix)

	var rows []map[string]string
	var err error
	switch {
	case req.Command != "":
		rows, err = t.api.Query(ctx, "SELECT m_iris.Eval(?,?) AS status", rid, req.Command)
	case req.EntryRef != "":
		rows, err = t.api.Query(ctx, "SELECT m_iris.RunRef(?,?,?) AS status",
			rid, req.EntryRef, strings.Join(req.Args, "\x01"))
	default:
		return mdriver.ExecResult{}, fmt.Errorf("remote: exec needs an entryref or a command")
	}
	if err != nil {
		return mdriver.ExecResult{}, err
	}

	status := firstCol(rows, "status")
	switch status {
	case "7":
		return mdriver.ExecResult{}, fmt.Errorf("remote: runner refused — caller lacks the m_iris_runner role / action-query privilege")
	case "5":
		eng, rerr := t.readEngineError(ctx, rid)
		if rerr != nil {
			return mdriver.ExecResult{}, rerr
		}
		return mdriver.ExecResult{Status: 5, EngineError: eng}, nil
	}

	out, err := t.getGlobal(ctx, fmt.Sprintf(`^mIrisRun(%q,"out")`, rid))
	if err != nil {
		return mdriver.ExecResult{}, err
	}
	st, _ := strconv.Atoi(status)
	return mdriver.ExecResult{Stdout: out, Status: st}, nil
}

// readEngineError reads ^mIrisRun(rid,"error") and parses the §7 frame
// "mnemonic|routine|line|text".
func (t *Transport) readEngineError(ctx context.Context, rid string) (*mdriver.EngineError, error) {
	raw, err := t.getGlobal(ctx, fmt.Sprintf(`^mIrisRun(%q,"error")`, rid))
	if err != nil {
		return nil, err
	}
	parts := strings.SplitN(raw, "|", 4)
	eng := &mdriver.EngineError{}
	if len(parts) > 0 {
		eng.Mnemonic = parts[0]
	}
	if len(parts) > 1 {
		eng.Routine = parts[1]
	}
	if len(parts) > 2 {
		eng.Line, _ = strconv.Atoi(parts[2])
	}
	if len(parts) > 3 {
		eng.Text = parts[3]
	}
	return eng, nil
}

// ReadGlobal reads a single global node via the runner (contract data.get).
func (t *Transport) ReadGlobal(ctx context.Context, req mdriver.GlobalRef) (mdriver.GlobalNode, error) {
	if err := t.ensureRunner(ctx); err != nil {
		return mdriver.GlobalNode{}, err
	}
	v, err := t.getGlobal(ctx, req.Ref)
	if err != nil {
		return mdriver.GlobalNode{}, err
	}
	return mdriver.GlobalNode{Ref: req.Ref, Value: v}, nil
}

// SetGlobal sets a single global node via the runner (contract data.set).
func (t *Transport) SetGlobal(ctx context.Context, ref, value string) error {
	if err := t.ensureRunner(ctx); err != nil {
		return err
	}
	if _, err := t.api.Query(ctx, "SELECT m_iris.SetGlobal(?,?) AS ok", ref, value); err != nil {
		return err
	}
	return nil
}

func (t *Transport) getGlobal(ctx context.Context, ref string) (string, error) {
	rows, err := t.api.Query(ctx, "SELECT m_iris.GetGlobal(?) AS value", ref)
	if err != nil {
		return "", err
	}
	return firstCol(rows, "value"), nil
}

// Load PUT+compiles routine source over Atelier (contract exec.load on remote).
// Compile diagnostics are surfaced as an EngineError rather than a Go error —
// a failed compile is a bad result, not a transport failure.
func (t *Transport) Load(ctx context.Context, req mdriver.LoadRequest) (mdriver.LoadResult, error) {
	files, err := expandPaths(req.Paths)
	if err != nil {
		return mdriver.LoadResult{}, err
	}
	var loaded []string
	for _, f := range files {
		content, rerr := os.ReadFile(f)
		if rerr != nil {
			return mdriver.LoadResult{}, rerr
		}
		name := req.Prefix + filepath.Base(f)
		if _, perr := t.api.PutDoc(ctx, name, splitLines(string(content))); perr != nil {
			return mdriver.LoadResult{}, perr
		}
		loaded = append(loaded, name)
	}
	if len(loaded) > 0 {
		if _, cerr := t.api.Compile(ctx, loaded, "cuk"); cerr != nil {
			return mdriver.LoadResult{}, cerr
		}
	}
	return mdriver.LoadResult{Loaded: loaded}, nil
}

// Health proves the remote substrate is reachable AND that the caller actually
// holds the action/query privilege (a SELECT 1, not just TCP reachability —
// risks C3, C7). Version enrichment lands with the M1 root-endpoint probe.
func (t *Transport) Health(ctx context.Context) (mdriver.Health, error) {
	rows, err := t.api.Query(ctx, "SELECT 1 AS one")
	if err != nil {
		return mdriver.Health{Running: false, Healthy: false}, err
	}
	healthy := firstCol(rows, "one") == "1"
	return mdriver.Health{Running: true, Healthy: healthy}, nil
}

func firstCol(rows []map[string]string, col string) string {
	if len(rows) == 0 {
		return ""
	}
	return rows[0][col]
}

func splitLines(s string) []string {
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

// expandPaths flattens files and directories into a routine-source file list.
func expandPaths(paths []string) ([]string, error) {
	var out []string
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			out = append(out, p)
			continue
		}
		entries, err := os.ReadDir(p)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if !e.IsDir() {
				out = append(out, filepath.Join(p, e.Name()))
			}
		}
	}
	return out, nil
}
