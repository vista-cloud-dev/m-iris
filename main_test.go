package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/vista-cloud-dev/irissync/clikit"
	"github.com/vista-cloud-dev/irissync/internal/config"
)

// fakeAtelier serves the read-side endpoints with the given routine contents
// and timestamps, in deterministic order.
func fakeAtelier(content map[string][]string, ts map[string]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/docnames/"):
			var sb strings.Builder
			sb.WriteString(`{"status":{"errors":[]},"result":{"content":[`)
			names := make([]string, 0, len(content))
			for n := range content {
				names = append(names, n)
			}
			sort.Strings(names)
			for i, name := range names {
				if i > 0 {
					sb.WriteByte(',')
				}
				fmt.Fprintf(&sb, `{"name":%q,"cat":"RTN","ts":%q}`, name, ts[name])
			}
			sb.WriteString(`]}}`)
			io.WriteString(w, sb.String())

		case strings.Contains(r.URL.Path, "/doc/"):
			// r.URL.Path is already percent-decoded by net/http, so "%25ZV.mac"
			// on the wire arrives here as "%ZV.mac".
			name := r.URL.Path[strings.Index(r.URL.Path, "/doc/")+len("/doc/"):]
			lines, ok := content[name]
			if !ok {
				w.WriteHeader(http.StatusInternalServerError)
				io.WriteString(w, `{"status":{"errors":[{"error":"does not exist"}]}}`)
				return
			}
			var sb strings.Builder
			fmt.Fprintf(&sb, `{"status":{"errors":[]},"result":{"name":%q,"ts":%q,"enc":false,"content":[`, name, ts[name])
			for i, l := range lines {
				if i > 0 {
					sb.WriteByte(',')
				}
				b, _ := json.Marshal(l)
				sb.Write(b)
			}
			sb.WriteString(`]}}`)
			io.WriteString(w, sb.String())

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func jsonCtx() (*clikit.Context, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	cc := clikit.NewContext(&clikit.Globals{Output: "json"}, "test")
	cc.Stdout = buf
	cc.Stderr = io.Discard
	return cc, buf
}

func exitOf(t *testing.T, err error) int {
	t.Helper()
	var e *clikit.Error
	if !errors.As(err, &e) {
		t.Fatalf("expected *clikit.Error, got %v", err)
	}
	return e.Exit
}

func TestReadRoundTrip(t *testing.T) {
	content := map[string][]string{
		"DGREG.mac": {"DGREG ;registration", " q"},
		"XUSER.mac": {"XUSER ;kernel", " q"},
		"%ZV.mac":   {"%ZV ;percent routine", " q"},
	}
	ts := map[string]string{
		"DGREG.mac": "2026-05-20 09:14:22.000",
		"XUSER.mac": "2026-05-19 17:02:10.000",
		"%ZV.mac":   "2026-05-18 08:00:00.000",
	}
	srv := fakeAtelier(content, ts)
	defer srv.Close()

	conn := &config.Conn{
		BaseURL:     srv.URL + "/api/atelier/v1/",
		Instance:    "test-inst",
		Namespace:   "VISTA",
		Mirror:      t.TempDir(),
		Concurrency: 4,
	}
	layout := conn.Layout()

	// status before any pull → all New → drift (exit 3).
	cc, _ := jsonCtx()
	if code := exitOf(t, (statusCmd{}).Run(cc, conn)); code != clikit.ExitCheck {
		t.Fatalf("fresh status exit = %d, want %d", code, clikit.ExitCheck)
	}

	// pull → writes all three routines + manifest.
	cc, _ = jsonCtx()
	if err := (&pullCmd{}).Run(cc, conn); err != nil {
		t.Fatalf("pull: %v", err)
	}
	got, err := os.ReadFile(layout.RoutinePath("DGREG.mac"))
	if err != nil {
		t.Fatalf("read mirror file: %v", err)
	}
	if string(got) != "DGREG ;registration\n q\n" {
		t.Errorf("DGREG.mac = %q", got)
	}
	if _, err := os.Stat(layout.RoutinePath("%ZV.mac")); err != nil {
		t.Errorf("percent routine not written: %v", err)
	}
	if _, err := os.Stat(layout.ManifestPath()); err != nil {
		t.Errorf("manifest not written: %v", err)
	}

	// status after pull → in sync (nil).
	cc, _ = jsonCtx()
	if err := (statusCmd{}).Run(cc, conn); err != nil {
		t.Fatalf("status after pull: %v (want in-sync)", err)
	}

	// verify after pull → clean (nil).
	cc, _ = jsonCtx()
	if err := (verifyCmd{}).Run(cc, conn); err != nil {
		t.Fatalf("verify after pull: %v (want clean)", err)
	}

	// Incremental: a second pull with no server change fetches nothing.
	cc, buf := jsonCtx()
	if err := (&pullCmd{}).Run(cc, conn); err != nil {
		t.Fatalf("second pull: %v", err)
	}
	var env struct {
		Data pullResult `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("decode pull envelope: %v\n%s", err, buf.String())
	}
	if env.Data.Fetched != 0 || env.Data.Unchanged != 3 {
		t.Errorf("incremental pull: fetched=%d unchanged=%d, want 0/3", env.Data.Fetched, env.Data.Unchanged)
	}

	// Tamper a mirror file → verify reports mismatch (exit 3).
	if err := os.WriteFile(layout.RoutinePath("XUSER.mac"), []byte("corrupted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cc, _ = jsonCtx()
	if code := exitOf(t, (verifyCmd{}).Run(cc, conn)); code != clikit.ExitCheck {
		t.Fatalf("verify after tamper exit = %d, want %d", code, clikit.ExitCheck)
	}
}

func TestPullDryRunWritesNothing(t *testing.T) {
	srv := fakeAtelier(
		map[string][]string{"A.mac": {"A ;x", " q"}},
		map[string]string{"A.mac": "2026-01-01 00:00:00.000"},
	)
	defer srv.Close()

	conn := &config.Conn{
		BaseURL: srv.URL + "/api/atelier/v1/", Instance: "i", Namespace: "VISTA",
		Mirror: t.TempDir(), Concurrency: 2, DryRun: true,
	}
	cc, _ := jsonCtx()
	if err := (&pullCmd{}).Run(cc, conn); err != nil {
		t.Fatalf("dry-run pull: %v", err)
	}
	if _, err := os.Stat(conn.Layout().ManifestPath()); !os.IsNotExist(err) {
		t.Errorf("dry run should not write a manifest (err=%v)", err)
	}
}

func TestFilterSelectsSubset(t *testing.T) {
	srv := fakeAtelier(
		map[string][]string{"DGREG.mac": {"x"}, "XUSER.mac": {"y"}},
		map[string]string{"DGREG.mac": "t1", "XUSER.mac": "t2"},
	)
	defer srv.Close()

	conn := &config.Conn{
		BaseURL: srv.URL + "/api/atelier/v1/", Instance: "i", Namespace: "VISTA",
		Mirror: t.TempDir(), Concurrency: 2, Filter: "DG*",
	}
	cc, _ := jsonCtx()
	if err := (&pullCmd{}).Run(cc, conn); err != nil {
		t.Fatalf("pull: %v", err)
	}
	if _, err := os.Stat(conn.Layout().RoutinePath("DGREG.mac")); err != nil {
		t.Errorf("DGREG.mac should be pulled: %v", err)
	}
	if _, err := os.Stat(conn.Layout().RoutinePath("XUSER.mac")); !os.IsNotExist(err) {
		t.Errorf("XUSER.mac should be filtered out (err=%v)", err)
	}
}
