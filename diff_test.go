package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vista-cloud-dev/m-iris/internal/config"
)

// diffResultOf runs `sync diff` and decodes the {unified} envelope.
func diffResultOf(t *testing.T, c *syncDiffCmd, conn *config.Conn) syncDiffResult {
	t.Helper()
	cc, buf := jsonCtx()
	if err := c.Run(cc, conn); err != nil {
		t.Fatalf("diff: %v", err)
	}
	var env struct {
		Data syncDiffResult `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v\n%s", err, buf.String())
	}
	return env.Data
}

func TestSyncDiff(t *testing.T) {
	content := map[string][]string{"DGREG.mac": {"DGREG ;reg", " w 1", " q"}}
	ts := map[string]string{"DGREG.mac": "t1"}
	srv := fakeAtelier(content, ts)
	defer srv.Close()

	conn := &config.Conn{
		BaseURL: srv.URL + "/api/atelier/v1/", Instance: "i", Namespace: "VISTA",
		Mirror: t.TempDir(), Concurrency: 2, Type: "mac",
	}
	// Pull so the mirror holds the instance copy.
	cc, _ := jsonCtx()
	if err := (&pullCmd{}).Run(cc, conn); err != nil {
		t.Fatalf("pull: %v", err)
	}

	// Identical → empty unified diff.
	if got := diffResultOf(t, &syncDiffCmd{Name: "DGREG"}, conn); got.Unified != "" {
		t.Errorf("identical diff should be empty, got:\n%s", got.Unified)
	}

	// Edit the mirror file → diff surfaces the change (instance vs mirror).
	path := conn.Layout().RoutinePath("DGREG.mac")
	if err := os.WriteFile(path, []byte("DGREG ;reg\n w 2\n q\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := diffResultOf(t, &syncDiffCmd{Name: "DGREG"}, conn)
	for _, want := range []string{"--- instance/DGREG.mac", "+++ mirror/DGREG.mac", "- w 1", "+ w 2"} {
		if !strings.Contains(got.Unified, want) {
			t.Errorf("missing %q in unified diff:\n%s", want, got.Unified)
		}
	}
}

func TestSyncDiffMissingInstance(t *testing.T) {
	// Instance has no such routine; the mirror/--from has one → pure addition.
	srv := fakeAtelier(map[string][]string{}, map[string]string{})
	defer srv.Close()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "NEW.mac"), []byte("NEW ;x\n q\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	conn := &config.Conn{
		BaseURL: srv.URL + "/api/atelier/v1/", Instance: "i", Namespace: "VISTA",
		Mirror: t.TempDir(), Type: "mac",
	}
	got := diffResultOf(t, &syncDiffCmd{Name: "NEW", From: dir}, conn)
	if !strings.Contains(got.Unified, "+NEW ;x") {
		t.Errorf("expected pure addition for a routine absent on the instance, got:\n%s", got.Unified)
	}
}
