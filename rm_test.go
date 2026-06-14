package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/vista-cloud-dev/m-iris/internal/config"
)

// fakeAtelierRm wraps fakeAtelier's read endpoints and records DELETEs so a test
// can assert the instance copy was removed.
func fakeAtelierRm(content map[string][]string, ts map[string]string, deleted *[]string) *httptest.Server {
	inner := fakeAtelier(content, ts)
	var mu sync.Mutex
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/doc/") {
			name := r.URL.Path[strings.Index(r.URL.Path, "/doc/")+len("/doc/"):]
			mu.Lock()
			*deleted = append(*deleted, name)
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":{"errors":[]},"result":{}}`))
			return
		}
		inner.Config.Handler.ServeHTTP(w, r)
	}))
}

func rmResultOf(t *testing.T, c *syncRmCmd, conn *config.Conn) syncRmResult {
	t.Helper()
	cc, buf := jsonCtx()
	if err := c.Run(cc, conn); err != nil {
		t.Fatalf("rm: %v", err)
	}
	var env struct {
		Data syncRmResult `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v\n%s", err, buf.String())
	}
	return env.Data
}

func TestSyncRm(t *testing.T) {
	content := map[string][]string{"DGREG.mac": {"DGREG ;reg", " q"}}
	ts := map[string]string{"DGREG.mac": "t1"}
	var deleted []string
	srv := fakeAtelierRm(content, ts, &deleted)
	defer srv.Close()

	conn := &config.Conn{
		BaseURL: srv.URL + "/api/atelier/v1/", Instance: "i", Namespace: "VISTA",
		Mirror: t.TempDir(), Concurrency: 2, Type: "mac",
	}
	cc, _ := jsonCtx()
	if err := (&pullCmd{}).Run(cc, conn); err != nil {
		t.Fatalf("pull: %v", err)
	}
	mirrorFile := conn.Layout().RoutinePath("DGREG.mac")
	if _, err := os.Stat(mirrorFile); err != nil {
		t.Fatalf("precondition: mirror file missing: %v", err)
	}

	// Dry run removes nothing.
	dry := rmResultOf(t, &syncRmCmd{Name: "DGREG"}, withDryRun(conn))
	if len(dry.Removed) != 1 || !dry.DryRun {
		t.Errorf("dry run: removed=%v dryRun=%v", dry.Removed, dry.DryRun)
	}
	if len(deleted) != 0 {
		t.Errorf("dry run must not DELETE on the instance, got %v", deleted)
	}
	if _, err := os.Stat(mirrorFile); err != nil {
		t.Errorf("dry run must not remove the mirror file: %v", err)
	}

	// Real rm removes from instance + mirror + manifest.
	got := rmResultOf(t, &syncRmCmd{Name: "DGREG"}, conn)
	if len(got.Removed) != 1 || got.Removed[0] != "DGREG.mac" {
		t.Errorf("removed = %v, want [DGREG.mac]", got.Removed)
	}
	if len(deleted) != 1 || deleted[0] != "DGREG.mac" {
		t.Errorf("instance DELETE = %v, want [DGREG.mac]", deleted)
	}
	if _, err := os.Stat(mirrorFile); !os.IsNotExist(err) {
		t.Errorf("mirror file should be gone, err=%v", err)
	}
}

func withDryRun(conn *config.Conn) *config.Conn {
	c := *conn
	c.DryRun = true
	return &c
}
