// Package remote is the IRIS `remote` transport: vendor logic that drives an
// IRIS namespace entirely over the Atelier REST API. Because Atelier has no raw
// "run ObjectScript" endpoint, every ObjectScript operation rides the
// m.iris.Runner class (runner/m.iris.Runner.cls): the transport PUT+compiles it
// once, then invokes its SQL-projected procedures via action/query and reads
// results back out of a result global. This is the entire remote substrate
// (driver-plan §5 task 8, risk B2); remote exec/data/cover/admin all sit on it.
package remote

import (
	"context"
	_ "embed"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	mdriver "github.com/vista-cloud-dev/m-driver-sdk"
	"github.com/vista-cloud-dev/m-iris/internal/atelier"
)

//go:embed runner/m.iris.Runner.cls
var runnerSource string

//go:embed runner/mIrisIO.int
var ioHelperSource string

// runnerDoc is the Atelier docname of the runner class. Package "m.iris" (dots,
// no underscore — IRIS class names forbid underscores) projects its SqlProcs
// into the SQL schema "m_iris", so the m_iris.* SQL calls below are unchanged.
const runnerDoc = "m.iris.Runner.cls"

// ioHelperDoc is the Atelier docname of the companion IO-capture routine the
// runner's RunRef/Eval call (start^mIrisIO / stop^mIrisIO) to redirect a
// script's principal-device WRITE output into ^mIrisRun(rid,"out"). It is a
// classic .int routine because %Device.ReDirectIO dispatches each WRITE to
// mnemonic-space *routine* labels (wstr/wchr/wnl/…), which a class method
// cannot host.
const ioHelperDoc = "mIrisIO.int"

// AtelierAPI is the slice of the Atelier client the remote transport needs. It
// is narrowed to an interface so unit tests inject a fake (recording PUT/Compile
// and scripting Query rows) without an HTTP server — the real *atelier.Client is
// the gated integration tier.
type AtelierAPI interface {
	PutDoc(ctx context.Context, name string, content []string) (*atelier.PutResult, error)
	Compile(ctx context.Context, names []string, flags string) (*atelier.CompileResult, error)
	Query(ctx context.Context, sql string, params ...string) ([]map[string]string, error)
	// CloseIdleConnections drops pooled keep-alive connections so a follow-up
	// query opens a fresh one (exec recovers a run's result over a clean process
	// after a device-corrupting install).
	CloseIdleConnections()
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
	ioLines := strings.Split(strings.TrimRight(ioHelperSource, "\n"), "\n")
	if _, err := t.api.PutDoc(ctx, ioHelperDoc, irisRoutineLines(ioHelperDoc, ioLines)); err != nil {
		return fmt.Errorf("remote: deploy IO helper: %w", err)
	}
	res, err := t.api.Compile(ctx, []string{runnerDoc, ioHelperDoc}, "cuk")
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

	var qerr error
	switch {
	case req.Command != "":
		_, qerr = t.api.Query(ctx, "SELECT m_iris.Eval(?,?) AS status", rid, req.Command)
	case req.EntryRef != "":
		_, qerr = t.api.Query(ctx, "SELECT m_iris.RunRef(?,?,?) AS status",
			rid, req.EntryRef, strings.Join(req.Args, "\x01"))
	default:
		return mdriver.ExecResult{}, fmt.Errorf("remote: exec needs an entryref or a command")
	}

	// The run records status/out/error in ^mIrisRun(rid,*) and sets "done" last.
	// A KIDS install (EN^XPDIJ) can corrupt THIS SqlProc's gateway process/device,
	// so the action/query returns an empty/lost body (qerr) AND that process keeps
	// spoiling responses for a moment — so don't trust qerr or the response row.
	// Recover the outcome from the globals, retrying on fresh connections until a
	// clean process serves the read; "done" gates it (missing → the run truly did
	// not run).
	status, out, eng, rerr := t.recoverRun(ctx, rid)
	if rerr != nil {
		if qerr != nil {
			return mdriver.ExecResult{}, qerr
		}
		return mdriver.ExecResult{}, rerr
	}
	switch status {
	case "7":
		return mdriver.ExecResult{}, fmt.Errorf("remote: runner refused — caller lacks the m_iris_runner role / action-query privilege")
	case "5":
		return mdriver.ExecResult{Status: 5, EngineError: eng}, nil
	}
	st, _ := strconv.Atoi(status)
	return mdriver.ExecResult{Stdout: out, Status: st}, nil
}

// recoverRun reads a run's outcome (status, captured out, §7 fault) from
// ^mIrisRun(rid,*) after the run query. A device-corrupting install spoils the
// gateway process/connection that served the run, so the first read(s) may come
// back empty; retry, dropping pooled connections each time so a fresh one lands
// on a clean process, until "done" is readable (or the budget is exhausted).
func (t *Transport) recoverRun(ctx context.Context, rid string) (status, out string, eng *mdriver.EngineError, err error) {
	doneRef := fmt.Sprintf(`^mIrisRun(%q,"done")`, rid)
	statusRef := fmt.Sprintf(`^mIrisRun(%q,"status")`, rid)
	var last error
	for attempt := 0; attempt < 20; attempt++ {
		t.api.CloseIdleConnections()
		done, derr := t.getGlobal(ctx, doneRef)
		if derr == nil && done == "1" {
			st, serr := t.getGlobal(ctx, statusRef)
			if serr != nil {
				return "", "", nil, serr
			}
			if st == "5" {
				e, eerr := t.readEngineError(ctx, rid)
				return "5", "", e, eerr
			}
			o, oerr := t.getOut(ctx, rid)
			if oerr != nil {
				return "", "", nil, oerr
			}
			return st, o, nil, nil
		}
		last = derr
		select {
		case <-ctx.Done():
			return "", "", nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	if last != nil {
		return "", "", nil, last
	}
	return "", "", nil, fmt.Errorf("remote: run did not complete (no result recorded for %q)", rid)
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

// Abort stops a run still in flight under the ephemeral prefix (contract
// exec.abort). The runner records each run's process and clears it on completion,
// so abort terminates a live, not-yet-done run and reports the pid; a synchronous
// run that has already returned leaves nothing to stop (killed is empty — parity
// with m-ydb's "no jobs matched"). Abort is a driver-local exec verb, not a
// neutral Transport method (the SDK Transport has no Abort) — like m-ydb's
// Session.Abort.
func (t *Transport) Abort(ctx context.Context, prefix string) ([]string, error) {
	if err := t.ensureRunner(ctx); err != nil {
		return nil, err
	}
	rows, err := t.api.Query(ctx, "SELECT m_iris.Abort(?) AS pid", prefix)
	if err != nil {
		return nil, err
	}
	pid := firstCol(rows, "pid")
	switch pid {
	case "DENIED":
		return nil, fmt.Errorf("remote: runner refused abort — caller lacks the m_iris_runner role / action-query privilege")
	case "":
		return nil, nil
	default:
		return []string{pid}, nil
	}
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

// KillGlobal kills a global node / subtree via the runner (contract data.kill).
func (t *Transport) KillGlobal(ctx context.Context, ref string) error {
	if err := t.ensureRunner(ctx); err != nil {
		return err
	}
	if _, err := t.api.Query(ctx, "SELECT m_iris.KillGlobal(?) AS ok", ref); err != nil {
		return err
	}
	return nil
}

// QueryGlobal walks the subtree rooted at ref and returns its contained nodes
// (contract data.query). The runner returns a node list — one line per node,
// "Base64(ref)<TAB>Base64(value)" — which parseNodes decodes. order is
// "forward"/"reverse"; depth>0 caps levels below ref (0 = the whole subtree).
func (t *Transport) QueryGlobal(ctx context.Context, ref, order string, depth int) ([]mdriver.GlobalNode, error) {
	if err := t.ensureRunner(ctx); err != nil {
		return nil, err
	}
	rows, err := t.api.Query(ctx, "SELECT m_iris.QueryGlobal(?,?,?) AS nodes", ref, order, strconv.Itoa(depth))
	if err != nil {
		return nil, err
	}
	return parseNodes(firstCol(rows, "nodes"))
}

// parseNodes decodes a runner/session node list ("Base64(ref)<TAB>Base64(value)"
// per line) into flat GlobalNodes.
func parseNodes(raw string) ([]mdriver.GlobalNode, error) {
	var nodes []mdriver.GlobalNode
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		tab := strings.IndexByte(line, '\t')
		if tab < 0 {
			continue
		}
		ref, err := base64.StdEncoding.DecodeString(line[:tab])
		if err != nil {
			return nil, fmt.Errorf("remote: decode query ref: %w", err)
		}
		val, err := base64.StdEncoding.DecodeString(line[tab+1:])
		if err != nil {
			return nil, fmt.Errorf("remote: decode query value: %w", err)
		}
		nodes = append(nodes, mdriver.GlobalNode{Ref: string(ref), Value: string(val)})
	}
	return nodes, nil
}

// getOut reads the captured result-global text for a run, UTF-8-then-Base64-encoded
// by the runner so control bytes (a KIDS install's ANSI/terminal output) survive
// the action/query JSON transport — a raw read truncates at the first non-text
// byte, dropping the trailing result markers v-pkg parses. The runner UTF-8-encodes
// first so wide (Unicode >255) output is byte-safe for Base64; the decoded bytes
// are UTF-8, which string(raw) turns straight into a Go string. IRIS Base64Encode
// may wrap the encoded text at 76 columns, so strip whitespace before decoding.
func (t *Transport) getOut(ctx context.Context, rid string) (string, error) {
	rows, err := t.api.Query(ctx, "SELECT m_iris.GetOut(?) AS out", rid)
	if err != nil {
		return "", err
	}
	b64 := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, firstCol(rows, "out"))
	if b64 == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", fmt.Errorf("remote: decode captured output: %w", err)
	}
	return string(raw), nil
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
		name := req.Prefix + irisDocname(filepath.Base(f))
		if _, perr := t.api.PutDoc(ctx, name, irisRoutineLines(name, splitLines(string(content)))); perr != nil {
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

// irisDocname maps a routine-source basename to a valid IRIS Atelier docname.
// The neutral routine extension ".m" (what m-cli / the SDK Client and v-pkg
// stage) is NOT an Atelier routine type, so a ".m" doc never stages and a
// later `exec run EN^<rtn>` cannot resolve it. Map it to ".int" — classic
// MUMPS intermediate code, matching the label + space-indented body the SDK
// routine-wrap emits. Names that already carry an IRIS extension
// (.mac/.int/.inc/.cls) pass through unchanged.
func irisDocname(base string) string {
	if strings.EqualFold(filepath.Ext(base), ".m") {
		return strings.TrimSuffix(base, filepath.Ext(base)) + ".int"
	}
	return base
}

// irisRoutineLines ensures a routine doc carries the UDL header Atelier requires
// as its first line — `ROUTINE <name> [Type=INT|MAC|INC]` — derived from the
// docname. Without it the server rejects the PUT (#16021 "Illegal Header Line")
// even though the body's first line is a valid routine label. A doc that already
// leads with a `ROUTINE ` header (e.g. one round-tripped out of IRIS) or a
// non-routine type (.cls carries its own `Class …` header) passes through
// unchanged.
func irisRoutineLines(docname string, lines []string) []string {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(docname), "."))
	switch ext {
	case "int", "mac", "inc":
	default:
		return lines
	}
	if len(lines) > 0 && strings.HasPrefix(lines[0], "ROUTINE ") {
		return lines
	}
	name := strings.TrimSuffix(filepath.Base(docname), filepath.Ext(docname))
	header := fmt.Sprintf("ROUTINE %s [Type=%s]", name, strings.ToUpper(ext))
	return append([]string{header}, lines...)
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
