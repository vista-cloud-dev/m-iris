package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vista-cloud-dev/m-iris/internal/atelier"
	"github.com/vista-cloud-dev/m-iris/internal/config"
)

// TestSyncAxis_RealEngine validates the M2 source verbs added in this milestone
// — push --from, diff, rm — against a REAL IRIS over Atelier, using an ephemeral
// scratch routine (zzMIRISIT prefix) so it attaches to an existing namespace
// without clobbering anything (attached-mode cleanup, driver-plan §5 nuance).
//
// Gated: runs only with M_IRIS_IT=1 and an Atelier target in M_IRIS_* env — the
// same vars the driver uses. The fake-Atelier unit tests cover every commit;
// this real tier is `make test-it` / CI. push uses --no-compile so the test
// exercises the source round-trip (PUT/GET/DELETE) the new verbs ride, not the
// compiler.
//
//	M_IRIS_IT=1 M_IRIS_BASE_URL=http://localhost:52774/api/atelier/v1/ \
//	M_IRIS_NAMESPACE=USER M_IRIS_USER=_SYSTEM M_IRIS_PASSWORD=testsys \
//	go test -run TestSyncAxis_RealEngine . -v
func TestSyncAxis_RealEngine(t *testing.T) {
	if os.Getenv("M_IRIS_IT") != "1" {
		t.Skip("set M_IRIS_IT=1 (+ M_IRIS_* connection env) to run the real-engine sync tier")
	}
	conn := &config.Conn{
		Transport:   "remote",
		BaseURL:     envOrDefault("M_IRIS_BASE_URL", "http://localhost:52774/api/atelier/v1/"),
		Namespace:   envOrDefault("M_IRIS_NAMESPACE", "USER"),
		Instance:    "m-test-iris",
		User:        envOrDefault("M_IRIS_USER", "_SYSTEM"),
		Password:    envOrDefault("M_IRIS_PASSWORD", "testsys"),
		Mirror:      t.TempDir(),
		Type:        "mac",
		Concurrency: 4,
		Filter:      "zzMIRISIT*", // scope every server listing to the scratch prefix
	}
	const bare = "zzMIRISITSCRATCH"
	docname := bare + ".mac"

	acfg, err := conn.Atelier()
	if err != nil {
		t.Fatalf("atelier config: %v", err)
	}
	client, err := atelier.New(acfg)
	if err != nil {
		t.Fatalf("atelier client: %v", err)
	}
	ctx := context.Background()
	cleanup := func() { _ = client.DeleteDoc(ctx, docname) }
	cleanup()          // drop a leftover from a prior failed run
	t.Cleanup(cleanup) // and on the way out

	// Seed the manifest with a scoped pull (matches nothing yet → empty manifest).
	cc, _ := jsonCtx()
	if err := (&pullCmd{}).Run(cc, conn); err != nil {
		t.Fatalf("seed pull: %v", err)
	}

	// push --from: create the scratch routine on the instance + mirror + manifest.
	from := t.TempDir()
	if err := os.WriteFile(filepath.Join(from, docname), []byte(bare+" ;m-iris IT scratch\n quit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cc, buf := jsonCtx()
	if err := (&pushCmd{From: from, NoCompile: true}).Run(cc, conn); err != nil {
		t.Fatalf("push --from: %v\n%s", err, buf.String())
	}
	var pe struct {
		Data pushResult `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &pe); err != nil {
		t.Fatalf("decode push: %v\n%s", err, buf.String())
	}
	if pe.Data.Pushed != 1 {
		t.Fatalf("pushed = %d, want 1\n%s", pe.Data.Pushed, buf.String())
	}
	if _, exists, sErr := client.Stat(ctx, docname); sErr != nil || !exists {
		t.Fatalf("scratch routine should exist on the instance: exists=%v err=%v", exists, sErr)
	}

	// diff: edit the mirror copy → the instance↔mirror diff surfaces the change.
	if err := os.WriteFile(conn.Layout().RoutinePath(docname), []byte(bare+" ;m-iris IT scratch\n ; edited\n quit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cc, buf = jsonCtx()
	if err := (&syncDiffCmd{Name: bare}).Run(cc, conn); err != nil {
		t.Fatalf("diff: %v", err)
	}
	var de struct {
		Data syncDiffResult `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &de); err != nil {
		t.Fatalf("decode diff: %v\n%s", err, buf.String())
	}
	if !strings.Contains(de.Data.Unified, "+ ; edited") {
		t.Errorf("diff should surface the local edit, got:\n%s", de.Data.Unified)
	}

	// rm: delete from the instance + mirror + manifest; the instance copy is gone.
	cc, _ = jsonCtx()
	if err := (&syncRmCmd{Name: bare}).Run(cc, conn); err != nil {
		t.Fatalf("rm: %v", err)
	}
	if _, exists, sErr := client.Stat(ctx, docname); sErr != nil || exists {
		t.Errorf("scratch routine should be removed: exists=%v err=%v", exists, sErr)
	}
}

func envOrDefault(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
