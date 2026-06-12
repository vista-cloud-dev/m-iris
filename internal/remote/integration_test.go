package remote

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mdriver "github.com/vista-cloud-dev/m-driver-sdk"
	"github.com/vista-cloud-dev/m-iris/internal/atelier"
)

// TestRemoteSpike_RealEngine is the REMOTE SPIKE (driver-plan §5 task 8): it
// proves, against a real IRIS, that the runner class deploys over Atelier and
// the whole remote substrate round-trips — set/get a global, Eval a command,
// and surface a real fault as a structured EngineError. Make this green once and
// every other remote feature (exec/data/cover/admin) is de-risked, because they
// all ride exactly this path.
//
// Gated: it only runs with M_IRIS_IT=1 and an Atelier target in M_IRIS_* env
// (the same connection vars the driver uses). The fake-API unit tests above run
// every commit; this real-engine tier is nightly/CI (containers are minutes).
//
//	M_IRIS_IT=1 \
//	M_IRIS_BASE_URL=http://localhost:52773/api/atelier/v1/ \
//	M_IRIS_NAMESPACE=USER M_IRIS_USER=_SYSTEM M_IRIS_PASSWORD=SYS \
//	go test ./internal/remote/ -run TestRemoteSpike_RealEngine -v
func TestRemoteSpike_RealEngine(t *testing.T) {
	if os.Getenv("M_IRIS_IT") != "1" {
		t.Skip("set M_IRIS_IT=1 (+ M_IRIS_* connection env) to run the real-engine remote spike")
	}
	base := envOr("M_IRIS_BASE_URL", "http://localhost:52773/api/atelier/v1/")
	ns := envOr("M_IRIS_NAMESPACE", "USER")
	client, err := atelier.New(atelier.Config{
		BaseURL:   base,
		Namespace: ns,
		User:      envOr("M_IRIS_USER", "_SYSTEM"),
		Password:  envOr("M_IRIS_PASSWORD", "SYS"),
		Timeout:   30 * time.Second,
	})
	if err != nil {
		t.Fatalf("atelier client: %v", err)
	}
	tr := New(client)
	ctx := context.Background()

	// Teardown: drop the test globals and the runner doc.
	t.Cleanup(func() {
		_, _ = client.Query(ctx, "SELECT m_iris.KillGlobal(?)", `^mIrisRun("zzit")`)
		_, _ = client.Query(ctx, "SELECT m_iris.KillGlobal(?)", `^mIrisIT`)
		_ = client.DeleteDoc(ctx, runnerDoc)
	})

	// 1. data set/get round-trips through the runner (deploys it on first use).
	if err := tr.SetGlobal(ctx, `^mIrisIT("ping")`, "pong"); err != nil {
		t.Fatalf("SetGlobal: %v", err)
	}
	node, err := tr.ReadGlobal(ctx, mdriver.GlobalRef{Ref: `^mIrisIT("ping")`})
	if err != nil {
		t.Fatalf("ReadGlobal: %v", err)
	}
	if node.Value != "pong" {
		t.Fatalf("global read-back = %q, want pong", node.Value)
	}

	// 2. Eval a command; its side effect is visible through a result-global read.
	if _, err := tr.Exec(ctx, mdriver.ExecRequest{
		Command: `set ^mIrisRun("zzit","out")="evaled"`, Prefix: "zzit",
	}); err != nil {
		t.Fatalf("Exec eval: %v", err)
	}
	out, err := tr.ReadGlobal(ctx, mdriver.GlobalRef{Ref: `^mIrisRun("zzit","out")`})
	if err != nil {
		t.Fatalf("ReadGlobal out: %v", err)
	}
	if out.Value != "evaled" {
		t.Fatalf("eval side effect = %q, want evaled", out.Value)
	}

	// 3. A deliberate fault surfaces as a structured EngineError, not a Go error.
	res, err := tr.Exec(ctx, mdriver.ExecRequest{
		Command: `set x=^mIrisNoSuchGlobal(1)`, Prefix: "zzfault",
	})
	if err != nil {
		t.Fatalf("fault Exec returned a Go error (should be data): %v", err)
	}
	if res.EngineError == nil || res.EngineError.Mnemonic == "" {
		t.Fatalf("expected a structured EngineError, got %+v", res)
	}
	t.Logf("engineError surfaced: %+v", res.EngineError)
}

// TestRemoteExecAxis_RealEngine proves the exec-axis additions end to end on a
// real IRIS: a neutral ".m" routine stages under a ".int" docname (fix: docname
// mapping), and running its entryref returns the routine's WRITE output as
// ExecResult.Stdout (fix: runner device-output capture via mIrisIO). Together
// these are what `v pkg install --engine iris` rides; before the fixes, Load
// staged an unresolvable ".m" doc and Stdout came back empty.
//
// Gated identically to the spike (M_IRIS_IT=1 + M_IRIS_* connection env).
func TestRemoteExecAxis_RealEngine(t *testing.T) {
	if os.Getenv("M_IRIS_IT") != "1" {
		t.Skip("set M_IRIS_IT=1 (+ M_IRIS_* connection env) to run the real-engine exec-axis test")
	}
	client, err := atelier.New(atelier.Config{
		BaseURL:   envOr("M_IRIS_BASE_URL", "http://localhost:52773/api/atelier/v1/"),
		Namespace: envOr("M_IRIS_NAMESPACE", "USER"),
		User:      envOr("M_IRIS_USER", "_SYSTEM"),
		Password:  envOr("M_IRIS_PASSWORD", "SYS"),
		Timeout:   30 * time.Second,
	})
	if err != nil {
		t.Fatalf("atelier client: %v", err)
	}
	tr := New(client)
	ctx := context.Background()

	// Stage a scratch routine (label + space-indented body — the SDK routine-wrap
	// shape) as a neutral ".m" file; Load must store it as ZZMIRISX.int.
	dir := t.TempDir()
	src := filepath.Join(dir, "ZZMIRISX.m")
	body := "ZZMIRISX ;m-iris exec-axis IT — safe to delete\nEN ;\n W \"<<IT>>ok=1\",!\n Q\n"
	if err := os.WriteFile(src, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = client.Query(ctx, "SELECT m_iris.KillGlobal(?)", `^mIrisRun("zzx")`)
		_ = client.DeleteDoc(ctx, "ZZMIRISX.int")
		_ = client.DeleteDoc(ctx, runnerDoc)
		_ = client.DeleteDoc(ctx, ioHelperDoc)
	})

	lr, err := tr.Load(ctx, mdriver.LoadRequest{Paths: []string{src}})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if lr.EngineError != nil {
		t.Fatalf("Load compile fault: %+v", lr.EngineError)
	}
	if len(lr.Loaded) != 1 || lr.Loaded[0] != "ZZMIRISX.int" {
		t.Fatalf("Loaded = %v, want [ZZMIRISX.int]", lr.Loaded)
	}

	res, err := tr.Exec(ctx, mdriver.ExecRequest{EntryRef: "EN^ZZMIRISX", Prefix: "zzx"})
	if err != nil {
		t.Fatalf("Exec run: %v", err)
	}
	if res.EngineError != nil {
		t.Fatalf("Exec fault: %+v", res.EngineError)
	}
	if !strings.Contains(res.Stdout, "<<IT>>ok=1") {
		t.Fatalf("Stdout = %q, want it to contain the routine's WRITE output <<IT>>ok=1", res.Stdout)
	}
}

// TestRemoteAbort_RealEngine proves exec.abort terminates a run still in flight
// on a real IRIS: one transport launches a long (`hang`) Eval that registers its
// process in ^mIrisRun(rid,"pid"); a second transport aborts it by prefix and
// reports the terminated pid; the launching call then returns promptly because
// its server process was killed. (Over the synchronous Atelier path a completed
// run leaves nothing to abort — this test is the positive, concurrent case.)
//
// Gated identically to the spike (M_IRIS_IT=1 + M_IRIS_* connection env).
func TestRemoteAbort_RealEngine(t *testing.T) {
	if os.Getenv("M_IRIS_IT") != "1" {
		t.Skip("set M_IRIS_IT=1 (+ M_IRIS_* connection env) to run the real-engine abort test")
	}
	cfg := atelier.Config{
		BaseURL:   envOr("M_IRIS_BASE_URL", "http://localhost:52773/api/atelier/v1/"),
		Namespace: envOr("M_IRIS_NAMESPACE", "USER"),
		User:      envOr("M_IRIS_USER", "_SYSTEM"),
		Password:  envOr("M_IRIS_PASSWORD", "SYS"),
		Timeout:   60 * time.Second,
	}
	// Two independent clients: one blocks on the long run, the other aborts it.
	runClient, err := atelier.New(cfg)
	if err != nil {
		t.Fatalf("atelier client (run): %v", err)
	}
	ctlClient, err := atelier.New(cfg)
	if err != nil {
		t.Fatalf("atelier client (ctl): %v", err)
	}
	runTr, ctlTr := New(runClient), New(ctlClient)
	ctx := context.Background()
	const rid = "zzMIRISABORT"

	t.Cleanup(func() {
		_, _ = ctlClient.Query(ctx, "SELECT m_iris.KillGlobal(?)", `^mIrisRun("`+rid+`")`)
	})

	// Pre-deploy the runner on BOTH transports before the timing-sensitive part:
	// ReadGlobal calls ensureRunner, so the later `hang` Eval registers its pid
	// immediately instead of racing a concurrent PUT+compile of the runner class.
	pidRef := `^mIrisRun("` + rid + `","pid")`
	if _, err := runTr.ReadGlobal(ctx, mdriver.GlobalRef{Ref: pidRef}); err != nil {
		t.Fatalf("warm up run transport: %v", err)
	}
	if _, err := ctlTr.ReadGlobal(ctx, mdriver.GlobalRef{Ref: pidRef}); err != nil {
		t.Fatalf("warm up ctl transport: %v", err)
	}

	// Launch a long-running run; it sets ^mIrisRun(rid,"pid")=$job then hangs.
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = runTr.Exec(ctx, mdriver.ExecRequest{Command: "hang 30", Prefix: rid})
	}()

	// Wait until the run has registered its process.
	var pid string
	for i := 0; i < 100; i++ {
		node, rerr := ctlTr.ReadGlobal(ctx, mdriver.GlobalRef{Ref: pidRef})
		if rerr == nil && node.Value != "" {
			pid = node.Value
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if pid == "" {
		t.Fatal("run never registered a pid — cannot test abort")
	}

	killed, err := ctlTr.Abort(ctx, rid)
	if err != nil {
		t.Fatalf("Abort: %v", err)
	}
	if len(killed) != 1 || killed[0] != pid {
		t.Fatalf("killed = %v, want [%s]", killed, pid)
	}

	// The terminated run's blocking call must return promptly now.
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("aborted run did not return after its process was terminated")
	}

	// A second abort finds nothing live (idempotent / honest).
	again, err := ctlTr.Abort(ctx, rid)
	if err != nil {
		t.Fatalf("second Abort: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("second abort killed = %v, want none", again)
	}
}

// TestRemoteData_RealEngine proves data.query/kill ride the runner on a real
// IRIS: a seeded subtree queries back exactly its contained nodes (a sibling is
// excluded by the $name containment walk), and kill removes the whole subtree.
//
// Gated identically to the spike (M_IRIS_IT=1 + M_IRIS_* connection env).
func TestRemoteData_RealEngine(t *testing.T) {
	if os.Getenv("M_IRIS_IT") != "1" {
		t.Skip("set M_IRIS_IT=1 (+ M_IRIS_* connection env) to run the real-engine data test")
	}
	client, err := atelier.New(atelier.Config{
		BaseURL:   envOr("M_IRIS_BASE_URL", "http://localhost:52773/api/atelier/v1/"),
		Namespace: envOr("M_IRIS_NAMESPACE", "USER"),
		User:      envOr("M_IRIS_USER", "_SYSTEM"),
		Password:  envOr("M_IRIS_PASSWORD", "SYS"),
		Timeout:   30 * time.Second,
	})
	if err != nil {
		t.Fatalf("atelier client: %v", err)
	}
	tr := New(client)
	ctx := context.Background()
	t.Cleanup(func() { _ = tr.KillGlobal(ctx, `^mDataIT`) })

	for ref, val := range map[string]string{
		`^mDataIT("a")`:       "1",
		`^mDataIT("a","sub")`: "2",
		`^mDataIT("b")`:       "3",
	} {
		if err := tr.SetGlobal(ctx, ref, val); err != nil {
			t.Fatalf("SetGlobal %s: %v", ref, err)
		}
	}
	q, err := tr.QueryGlobal(ctx, `^mDataIT("a")`, "forward", 0)
	if err != nil {
		t.Fatalf("QueryGlobal: %v", err)
	}
	if len(q) != 2 {
		t.Fatalf("query ^mDataIT(\"a\") = %d nodes, want 2 (excl. \"b\"): %+v", len(q), q)
	}
	if err := tr.KillGlobal(ctx, `^mDataIT("a")`); err != nil {
		t.Fatalf("KillGlobal: %v", err)
	}
	whole, err := tr.QueryGlobal(ctx, `^mDataIT`, "forward", 0)
	if err != nil {
		t.Fatalf("QueryGlobal whole: %v", err)
	}
	if len(whole) != 1 || whole[0].Ref != `^mDataIT("b")` {
		t.Fatalf("post-kill whole = %+v, want only ^mDataIT(\"b\")", whole)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
