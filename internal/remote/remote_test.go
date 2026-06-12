package remote

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mdriver "github.com/vista-cloud-dev/m-driver-sdk"
	"github.com/vista-cloud-dev/m-iris/internal/atelier"
)

// fakeAPI scripts the runner's SQL surface in-memory: it records PUT/Compile
// (so we can assert the runner is deployed exactly once) and answers Query by
// dispatching on the SQL + bound parameters, modelling ^mIrisRun.
type fakeAPI struct {
	puts     []string
	putBody  map[string][]string // docname → content (last PUT)
	compiles [][]string
	globals  map[string]string // global ref → value (the result global)
	runFault *clikit3Engine    // if set, the next RunRef faults with this frame
}

// clikit3Engine mirrors the runner's "mnemonic|routine|line|text" error frame.
type clikit3Engine struct{ mnemonic, routine, line, text string }

func newFakeAPI() *fakeAPI {
	return &fakeAPI{globals: map[string]string{}, putBody: map[string][]string{}}
}

// PutDoc models the real Atelier rejection a fake without it misses: a routine
// doc (.int/.mac/.inc) whose first line is not a `ROUTINE … [Type=…]` UDL header
// is refused #16021 ("Illegal Header Line"), so the transport MUST add the header
// (irisRoutineLines) before staging.
func (f *fakeAPI) PutDoc(_ context.Context, name string, content []string) (*atelier.PutResult, error) {
	switch ext(name) {
	case "int", "mac", "inc":
		if len(content) == 0 || !strings.HasPrefix(content[0], "ROUTINE ") {
			return nil, fmt.Errorf("atelier: PUT %q rejected: ERROR #16021: Illegal Header Line", name)
		}
	}
	f.puts = append(f.puts, name)
	f.putBody[name] = content
	return &atelier.PutResult{Name: name}, nil
}

func (f *fakeAPI) CloseIdleConnections() {}

func ext(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return strings.ToLower(name[i+1:])
	}
	return ""
}

func (f *fakeAPI) Compile(_ context.Context, names []string, _ string) (*atelier.CompileResult, error) {
	f.compiles = append(f.compiles, names)
	return &atelier.CompileResult{}, nil
}

func (f *fakeAPI) Query(_ context.Context, sql string, params ...string) ([]map[string]string, error) {
	switch {
	case strings.Contains(sql, "RunRef"):
		rid := params[0]
		f.globals[`^mIrisRun("`+rid+`","done")`] = "1"
		if f.runFault != nil {
			f.globals[`^mIrisRun("`+rid+`","status")`] = "5"
			f.globals[`^mIrisRun("`+rid+`","error")`] = strings.Join(
				[]string{f.runFault.mnemonic, f.runFault.routine, f.runFault.line, f.runFault.text}, "|")
			return []map[string]string{{"status": "5"}}, nil
		}
		f.globals[`^mIrisRun("`+rid+`","status")`] = "0"
		return []map[string]string{{"status": "0"}}, nil
	case strings.Contains(sql, "Eval"):
		rid := params[0]
		f.globals[`^mIrisRun("`+rid+`","done")`] = "1"
		f.globals[`^mIrisRun("`+rid+`","status")`] = "0"
		return []map[string]string{{"status": "0"}}, nil
	case strings.Contains(sql, "GetOut"):
		rid := params[0]
		enc := base64.StdEncoding.EncodeToString([]byte(f.globals[`^mIrisRun("`+rid+`","out")`]))
		return []map[string]string{{"out": enc}}, nil
	case strings.Contains(sql, "SetGlobal"):
		f.globals[params[0]] = params[1]
		return []map[string]string{{"ok": "1"}}, nil
	case strings.Contains(sql, "GetGlobal"):
		return []map[string]string{{"value": f.globals[params[0]]}}, nil
	case strings.Contains(sql, "SELECT 1"):
		return []map[string]string{{"one": "1"}}, nil
	}
	return nil, nil
}

// TestRemoteExec_DeploysRunnerOnceAndRunsClean proves the spike round-trip: the
// runner is PUT+compiled on first use (once), and a clean RunRef returns status 0.
func TestRemoteExec_DeploysRunnerOnceAndRunsClean(t *testing.T) {
	api := newFakeAPI()
	tr := New(api)
	ctx := context.Background()

	res, err := tr.Exec(ctx, mdriver.ExecRequest{EntryRef: "RUN^STDHARN", Prefix: "zzt42"})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if res.Status != 0 || res.EngineError != nil {
		t.Errorf("clean run = %+v, want status 0 no engineError", res)
	}
	// Runner + IO helper deployed exactly once, in one compile...
	if len(api.puts) != 2 || api.puts[0] != runnerDoc || api.puts[1] != ioHelperDoc {
		t.Errorf("puts = %v, want [%s %s]", api.puts, runnerDoc, ioHelperDoc)
	}
	if len(api.compiles) != 1 {
		t.Errorf("compiles = %v, want one", api.compiles)
	}
	// ...and not re-deployed on a second call.
	if _, err := tr.Exec(ctx, mdriver.ExecRequest{EntryRef: "OTHER^RTN", Prefix: "zzt42"}); err != nil {
		t.Fatalf("second Exec: %v", err)
	}
	if len(api.puts) != 2 {
		t.Errorf("runner re-deployed: puts = %v", api.puts)
	}
}

// TestRemoteExec_FaultBecomesEngineError proves a runtime fault flows back out of
// the result global as a structured §7 EngineError (not a Go error) — the whole
// point of routing remote exec through the runner.
func TestRemoteExec_FaultBecomesEngineError(t *testing.T) {
	api := newFakeAPI()
	api.runFault = &clikit3Engine{mnemonic: "<UNDEFINED>", routine: "XLFISO", line: "12", text: "global undefined"}
	tr := New(api)

	res, err := tr.Exec(context.Background(), mdriver.ExecRequest{EntryRef: "BROKEN^XLFISO", Prefix: "zzt7"})
	if err != nil {
		t.Fatalf("a fault must be data, not a Go error: %v", err)
	}
	if res.EngineError == nil {
		t.Fatal("expected an EngineError")
	}
	if res.EngineError.Mnemonic != "<UNDEFINED>" || res.EngineError.Routine != "XLFISO" || res.EngineError.Line != 12 {
		t.Errorf("engineError = %+v, want <UNDEFINED> XLFISO:12", res.EngineError)
	}
}

// TestLoad_MapsDotMToIntDocname proves Load stages a neutral ".m" routine
// source under a valid IRIS routine docname (".int", classic MUMPS) — ".m" is
// not an Atelier routine extension, so a docname kept as "ZVPKGINS.m" would
// never stage and `exec run EN^ZVPKGINS` would then fail to resolve. Other
// extensions (already-valid IRIS docnames) pass through unchanged.
func TestLoad_MapsDotMToIntDocname(t *testing.T) {
	dir := t.TempDir()
	dotM := filepath.Join(dir, "ZVPKGINS.m")
	if err := os.WriteFile(dotM, []byte("ZVPKGINS ;gen\nEN ;\n Q\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dotMac := filepath.Join(dir, "ALREADY.mac")
	if err := os.WriteFile(dotMac, []byte("ALREADY ;x\n q\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	api := newFakeAPI()
	tr := New(api)
	res, err := tr.Load(context.Background(), mdriver.LoadRequest{Paths: []string{dotM, dotMac}})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	wantLoaded := []string{"ZVPKGINS.int", "ALREADY.mac"}
	if len(res.Loaded) != 2 || res.Loaded[0] != wantLoaded[0] || res.Loaded[1] != wantLoaded[1] {
		t.Errorf("Loaded = %v, want %v", res.Loaded, wantLoaded)
	}
	// The runner is PUT once (ensureRunner) plus the two staged docs; assert the
	// staged docnames, not the runner doc.
	staged := api.puts[len(api.puts)-2:]
	if staged[0] != "ZVPKGINS.int" {
		t.Errorf("staged .m doc = %q, want ZVPKGINS.int", staged[0])
	}
	if staged[1] != "ALREADY.mac" {
		t.Errorf("staged .mac doc = %q, want ALREADY.mac (unchanged)", staged[1])
	}
	// The staged .int must lead with the UDL routine header (else Atelier #16021).
	if got := api.putBody["ZVPKGINS.int"][0]; got != "ROUTINE ZVPKGINS [Type=INT]" {
		t.Errorf("ZVPKGINS.int header = %q, want ROUTINE ZVPKGINS [Type=INT]", got)
	}
}

// TestRemoteData_SetGetRoundTrip proves data.set/get ride the same substrate.
func TestRemoteData_SetGetRoundTrip(t *testing.T) {
	api := newFakeAPI()
	tr := New(api)
	ctx := context.Background()

	ref := `^mIrisFix("k")`
	if err := tr.SetGlobal(ctx, ref, "hello"); err != nil {
		t.Fatalf("SetGlobal: %v", err)
	}
	node, err := tr.ReadGlobal(ctx, mdriver.GlobalRef{Ref: ref})
	if err != nil {
		t.Fatalf("ReadGlobal: %v", err)
	}
	if node.Value != "hello" {
		t.Errorf("read-back = %q, want hello", node.Value)
	}
}

// TestRemoteHealth_ProbesQueryPrivilege proves Health asserts the action/query
// privilege (SELECT 1), not just TCP reachability.
func TestRemoteHealth_ProbesQueryPrivilege(t *testing.T) {
	tr := New(newFakeAPI())
	h, err := tr.Health(context.Background())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if !h.Running || !h.Healthy {
		t.Errorf("health = %+v, want running+healthy", h)
	}
}
