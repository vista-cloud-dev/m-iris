package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mdriver "github.com/vista-cloud-dev/m-driver-sdk"
)

// TestSessionAxis_RealEngine drives the local/docker `iris session` transport end
// to end against a real IRIS container: health/version, load (.m → .int + compile)
// + run with device-output capture, eval (clean + a fault → §7 EngineError), a
// data set/get round-trip through a control byte, and abort of a run still in
// flight. The fake-run unit tests above run every commit; this tier needs a live
// container, gated on M_IRIS_IT=1 + M_IRIS_CONTAINER.
//
//	M_IRIS_IT=1 M_IRIS_CONTAINER=m-test-iris M_IRIS_NAMESPACE=USER \
//	go test ./internal/session/ -run TestSessionAxis_RealEngine -v
func TestSessionAxis_RealEngine(t *testing.T) {
	if os.Getenv("M_IRIS_IT") != "1" {
		t.Skip("set M_IRIS_IT=1 (+ M_IRIS_CONTAINER / M_IRIS_NAMESPACE) to run the session real-engine tier")
	}
	container := os.Getenv("M_IRIS_CONTAINER")
	if container == "" {
		t.Skip("set M_IRIS_CONTAINER to the IRIS docker container name")
	}
	cfg := Config{
		Transport: mdriver.TransportDocker,
		Container: container,
		Instance:  envOr("M_IRIS_IRIS_INSTANCE", "IRIS"),
		Namespace: envOr("M_IRIS_NAMESPACE", "USER"),
	}
	s := New(cfg, nil)
	ctx := context.Background()

	// 1. Health carries the IRIS version.
	h, err := s.Health(ctx)
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if !h.Healthy || h.Version == "" {
		t.Fatalf("health = %+v, want healthy with a version", h)
	}
	t.Logf("IRIS version %s", h.Version)

	// 2. Load a neutral .m routine (staged as .int + compiled) and run it; its
	// WRITE output is captured directly off the session's principal device.
	dir := t.TempDir()
	src := filepath.Join(dir, "ZZSESIT.m")
	body := "ZZSESIT ;session IT — safe to delete\nEN ;\n W \"<<SESIT>>ok=1\",!\n Q\n"
	if err := os.WriteFile(src, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = s.Exec(ctx, mdriver.ExecRequest{Command: `do $system.OBJ.Delete("ZZSESIT.int")`})
		_ = s.SetGlobal(ctx, `^mSesIT`, "") // touch, then kill below
		_, _ = s.Exec(ctx, mdriver.ExecRequest{Command: `kill ^mSesIT,^mIrisRun("zzsesabort")`})
	})

	lr, err := s.Load(ctx, mdriver.LoadRequest{Paths: []string{src}})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if lr.EngineError != nil {
		t.Fatalf("Load compile fault: %+v", lr.EngineError)
	}
	if len(lr.Loaded) != 1 || lr.Loaded[0] != "ZZSESIT.int" {
		t.Fatalf("Loaded = %v, want [ZZSESIT.int]", lr.Loaded)
	}
	run, err := s.Exec(ctx, mdriver.ExecRequest{EntryRef: "EN^ZZSESIT"})
	if err != nil {
		t.Fatalf("Exec run: %v", err)
	}
	if run.EngineError != nil {
		t.Fatalf("run fault: %+v", run.EngineError)
	}
	if !strings.Contains(run.Stdout, "<<SESIT>>ok=1") {
		t.Fatalf("run Stdout = %q, want it to contain <<SESIT>>ok=1", run.Stdout)
	}

	// 3. eval — clean + a deliberate fault → structured EngineError, not a Go error.
	ev, err := s.Exec(ctx, mdriver.ExecRequest{Command: `write "sum=",6*7`})
	if err != nil {
		t.Fatalf("Exec eval: %v", err)
	}
	if ev.Stdout != "sum=42" {
		t.Errorf("eval Stdout = %q, want sum=42", ev.Stdout)
	}
	fa, err := s.Exec(ctx, mdriver.ExecRequest{Command: `set x=^mNoSuchSes(1)`})
	if err != nil {
		t.Fatalf("fault eval returned a Go error: %v", err)
	}
	if fa.EngineError == nil || fa.EngineError.Mnemonic == "" {
		t.Fatalf("expected a structured EngineError, got %+v", fa)
	}

	// 4. data set/get round-trips through a control byte (base64-safe capture).
	want := "round\x01trip"
	if err := s.SetGlobal(ctx, `^mSesIT("k")`, want); err != nil {
		t.Fatalf("SetGlobal: %v", err)
	}
	node, err := s.ReadGlobal(ctx, mdriver.GlobalRef{Ref: `^mSesIT("k")`})
	if err != nil {
		t.Fatalf("ReadGlobal: %v", err)
	}
	if node.Value != want {
		t.Errorf("read-back = %q, want %q", node.Value, want)
	}

	// 4b. query walks the subtree (and excludes a sibling); kill removes it.
	if err := s.SetGlobal(ctx, `^mSesIT("k","sub")`, "deep"); err != nil {
		t.Fatalf("SetGlobal sub: %v", err)
	}
	if err := s.SetGlobal(ctx, `^mSesIT("z")`, "sibling"); err != nil {
		t.Fatalf("SetGlobal sibling: %v", err)
	}
	q, err := s.QueryGlobal(ctx, `^mSesIT("k")`, "forward", 0)
	if err != nil {
		t.Fatalf("QueryGlobal: %v", err)
	}
	if len(q) != 2 {
		t.Fatalf("query ^mSesIT(\"k\") returned %d nodes, want 2 (excl. the \"z\" sibling): %+v", len(q), q)
	}
	if err := s.KillGlobal(ctx, `^mSesIT("k")`); err != nil {
		t.Fatalf("KillGlobal: %v", err)
	}
	if after, _ := s.QueryGlobal(ctx, `^mSesIT("k")`, "forward", 0); len(after) != 0 {
		t.Errorf("post-kill query = %+v, want empty", after)
	}

	// 5. abort a run still in flight (a prefixed `hang` registers its $job; abort
	// terminates it). Two sessions: one hangs, one aborts.
	const rid = "zzsesabort"
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = s.Exec(ctx, mdriver.ExecRequest{Command: "hang 30", Prefix: rid})
	}()
	var pid string
	for i := 0; i < 100; i++ {
		n, rerr := s.ReadGlobal(ctx, mdriver.GlobalRef{Ref: `^mIrisRun("` + rid + `","pid")`})
		if rerr == nil && n.Value != "" {
			pid = n.Value
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if pid == "" {
		t.Fatal("prefixed run never registered a pid")
	}
	killed, err := s.Abort(ctx, rid)
	if err != nil {
		t.Fatalf("Abort: %v", err)
	}
	if len(killed) != 1 || killed[0] != pid {
		t.Fatalf("killed = %v, want [%s]", killed, pid)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("aborted run did not return after termination")
	}
	if again, _ := s.Abort(ctx, rid); len(again) != 0 {
		t.Errorf("second abort killed = %v, want none", again)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
