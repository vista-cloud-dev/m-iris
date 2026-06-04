package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/vista-cloud-dev/m-iris/clikit"
	"github.com/vista-cloud-dev/m-iris/internal/config"
)

// rwAtelier is a read+write fake Atelier server: it serves docnames, GET doc,
// PUT doc (which bumps the doc's timestamp), and action/compile. It is the
// write-side analog of fakeAtelier and lets the push tests exercise the full
// conflict-check / lock / compile-on-import path without a live server.
type rwAtelier struct {
	mu         sync.Mutex
	content    map[string][]string
	ts         map[string]string
	upd        map[string]bool // docname → updatable; absent = updatable
	tsSeq      int
	compiled   [][]string
	puts       []string
	compileErr string // when set, action/compile returns this diagnostic
}

func newRWAtelier(content map[string][]string, ts map[string]string) *rwAtelier {
	return &rwAtelier{content: content, ts: ts, upd: map[string]bool{}}
}

func (s *rwAtelier) updatable(name string) bool {
	if v, ok := s.upd[name]; ok {
		return v
	}
	return true
}

func (s *rwAtelier) start() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")

		switch {
		case strings.Contains(r.URL.Path, "/docnames/"):
			names := make([]string, 0, len(s.content))
			for n := range s.content {
				names = append(names, n)
			}
			sort.Strings(names)
			var sb strings.Builder
			sb.WriteString(`{"status":{"errors":[]},"result":{"content":[`)
			for i, name := range names {
				if i > 0 {
					sb.WriteByte(',')
				}
				fmt.Fprintf(&sb, `{"name":%q,"cat":"RTN","ts":%q,"upd":%t}`, name, s.ts[name], s.updatable(name))
			}
			sb.WriteString(`]}}`)
			_, _ = io.WriteString(w, sb.String())

		case strings.Contains(r.URL.Path, "/action/compile"):
			body, _ := io.ReadAll(r.Body)
			var names []string
			_ = json.Unmarshal(body, &names)
			s.compiled = append(s.compiled, names)
			if s.compileErr != "" {
				fmt.Fprintf(w, `{"status":{"errors":[{"error":%q,"code":"compile"}]},"result":{"content":[]}}`, s.compileErr)
				return
			}
			_, _ = io.WriteString(w, `{"status":{"errors":[]},"console":["done"],"result":{"content":[]}}`)

		case strings.Contains(r.URL.Path, "/doc/"):
			name := r.URL.Path[strings.Index(r.URL.Path, "/doc/")+len("/doc/"):]
			switch r.Method {
			case http.MethodGet:
				lines, ok := s.content[name]
				if !ok {
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = io.WriteString(w, `{"status":{"errors":[{"error":"ERROR #5002: doc does not exist","code":"5002"}]}}`)
					return
				}
				writeDoc(w, name, s.ts[name], lines)
			case http.MethodPut:
				body, _ := io.ReadAll(r.Body)
				var put struct {
					Content []string `json:"content"`
				}
				_ = json.Unmarshal(body, &put)
				s.tsSeq++
				newTS := fmt.Sprintf("2026-06-01 00:00:%02d.000", s.tsSeq)
				s.content[name] = put.Content
				s.ts[name] = newTS
				s.puts = append(s.puts, name)
				writeDoc(w, name, newTS, put.Content)
			default:
				w.WriteHeader(http.StatusMethodNotAllowed)
			}

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func writeDoc(w http.ResponseWriter, name, ts string, lines []string) {
	var sb strings.Builder
	fmt.Fprintf(&sb, `{"status":{"errors":[]},"result":{"name":%q,"ts":%q,"enc":false,"content":[`, name, ts)
	for i, l := range lines {
		if i > 0 {
			sb.WriteByte(',')
		}
		b, _ := json.Marshal(l)
		sb.Write(b)
	}
	sb.WriteString(`]}}`)
	_, _ = io.WriteString(w, sb.String())
}

// pullThenConn pulls a fresh mirror and returns the conn for reuse.
func pullThenConn(t *testing.T, srv *httptest.Server) *config.Conn {
	t.Helper()
	conn := &config.Conn{
		BaseURL: srv.URL + "/api/atelier/v1/", Instance: "test-inst", Namespace: "VISTA",
		Mirror: t.TempDir(), Concurrency: 4, Type: "mac",
	}
	cc, _ := jsonCtx()
	if err := (&pullCmd{}).Run(cc, conn); err != nil {
		t.Fatalf("pull: %v", err)
	}
	return conn
}

// TestG3RoundTrip is the gate-G3 proof: pull → edit → push → verify reproduces
// server state, and a second status shows no drift.
func TestG3RoundTrip(t *testing.T) {
	fake := newRWAtelier(
		map[string][]string{
			"DGREG.mac": {"DGREG ;registration", " q"},
			"XUSER.mac": {"XUSER ;kernel", " q"},
		},
		map[string]string{
			"DGREG.mac": "2026-05-20 09:14:22.000",
			"XUSER.mac": "2026-05-19 17:02:10.000",
		},
	)
	srv := fake.start()
	defer srv.Close()

	conn := pullThenConn(t, srv)
	layout := conn.Layout()

	// Edit one mirror file locally (the developer's change).
	edited := "DGREG ;registration\n ; edited locally\n q\n"
	if err := os.WriteFile(layout.RoutinePath("DGREG.mac"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	// push → writes the edited routine back, compiles, refreshes the manifest.
	cc, buf := jsonCtx()
	if err := (&pushCmd{}).Run(cc, conn); err != nil {
		t.Fatalf("push: %v", err)
	}
	var env struct {
		Data pushResult `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("decode push envelope: %v\n%s", err, buf.String())
	}
	if env.Data.Pushed != 1 {
		t.Errorf("pushed = %d, want 1 (only the edited routine)", env.Data.Pushed)
	}
	if env.Data.UpToDate != 1 {
		t.Errorf("upToDate = %d, want 1 (the unedited routine)", env.Data.UpToDate)
	}
	if !env.Data.Compiled {
		t.Error("expected compile-on-import")
	}

	// The server now holds the edited content.
	if got := strings.Join(fake.content["DGREG.mac"], "\n"); got != "DGREG ;registration\n ; edited locally\n q" {
		t.Errorf("server content after push = %q", got)
	}
	if len(fake.compiled) != 1 || len(fake.compiled[0]) != 1 || fake.compiled[0][0] != "DGREG.mac" {
		t.Errorf("compile calls = %v", fake.compiled)
	}

	// verify → clean (the manifest was refreshed to match the pushed file).
	cc, _ = jsonCtx()
	if err := (verifyCmd{}).Run(cc, conn); err != nil {
		t.Fatalf("verify after push: %v (want clean)", err)
	}

	// status → in sync (the manifest matches the new server timestamp).
	cc, _ = jsonCtx()
	if err := (statusCmd{}).Run(cc, conn); err != nil {
		t.Fatalf("status after push: %v (want in-sync)", err)
	}

	// A re-pull fetches nothing (server == manifest).
	cc, buf = jsonCtx()
	if err := (&pullCmd{}).Run(cc, conn); err != nil {
		t.Fatalf("re-pull: %v", err)
	}
	var pe struct {
		Data pullResult `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &pe); err != nil {
		t.Fatal(err)
	}
	if pe.Data.Fetched != 0 {
		t.Errorf("re-pull fetched = %d, want 0 (round-trip reproduced)", pe.Data.Fetched)
	}
}

// TestPushConflictRefuses proves the conflict-check: when the server changes a
// routine after pull, push refuses it with exit 4 and does not write.
func TestPushConflictRefuses(t *testing.T) {
	fake := newRWAtelier(
		map[string][]string{"A.mac": {"A ;v1", " q"}},
		map[string]string{"A.mac": "2026-05-20 09:14:22.000"},
	)
	srv := fake.start()
	defer srv.Close()

	conn := pullThenConn(t, srv)
	layout := conn.Layout()

	// Edit locally...
	if err := os.WriteFile(layout.RoutinePath("A.mac"), []byte("A ;mine\n q\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// ...and have someone else change it on the server out-of-band.
	fake.mu.Lock()
	fake.content["A.mac"] = []string{"A ;theirs", " q"}
	fake.ts["A.mac"] = "2026-05-27 10:00:00.000"
	fake.mu.Unlock()

	cc, _ := jsonCtx()
	err := (&pushCmd{}).Run(cc, conn)
	if code := exitOf(t, err); code != clikit.ExitRefused {
		t.Fatalf("push exit = %d, want %d (refused)", code, clikit.ExitRefused)
	}
	// The server copy must be untouched (no clobber).
	fake.mu.Lock()
	got := strings.Join(fake.content["A.mac"], "\n")
	puts := append([]string(nil), fake.puts...)
	fake.mu.Unlock()
	if got != "A ;theirs\n q" {
		t.Errorf("server content = %q, want the out-of-band edit preserved", got)
	}
	if len(puts) != 0 {
		t.Errorf("expected no PUTs on a refused push, got %v", puts)
	}
}

// TestPushForceOverridesConflict proves --force writes despite a server change.
func TestPushForceOverridesConflict(t *testing.T) {
	fake := newRWAtelier(
		map[string][]string{"A.mac": {"A ;v1", " q"}},
		map[string]string{"A.mac": "2026-05-20 09:14:22.000"},
	)
	srv := fake.start()
	defer srv.Close()

	conn := pullThenConn(t, srv)
	if err := os.WriteFile(conn.Layout().RoutinePath("A.mac"), []byte("A ;mine\n q\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	fake.ts["A.mac"] = "2026-05-27 10:00:00.000"
	fake.mu.Unlock()

	cc, _ := jsonCtx()
	if err := (&pushCmd{Force: true}).Run(cc, conn); err != nil {
		t.Fatalf("forced push: %v", err)
	}
	fake.mu.Lock()
	got := strings.Join(fake.content["A.mac"], "\n")
	fake.mu.Unlock()
	if got != "A ;mine\n q" {
		t.Errorf("server content after --force = %q, want the local edit", got)
	}
}

// TestPushDeferOnNonUpdatable proves detect-and-defer: a routine the server
// marks non-updatable (held by another writer) is deferred, not pushed.
func TestPushDeferOnNonUpdatable(t *testing.T) {
	fake := newRWAtelier(
		map[string][]string{"A.mac": {"A ;v1", " q"}},
		map[string]string{"A.mac": "2026-05-20 09:14:22.000"},
	)
	fake.upd["A.mac"] = false // held by another writer
	srv := fake.start()
	defer srv.Close()

	conn := pullThenConn(t, srv)
	if err := os.WriteFile(conn.Layout().RoutinePath("A.mac"), []byte("A ;mine\n q\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cc, buf := jsonCtx()
	err := (&pushCmd{}).Run(cc, conn)
	if code := exitOf(t, err); code != clikit.ExitRefused {
		t.Fatalf("push exit = %d, want %d (deferred → refused)", code, clikit.ExitRefused)
	}
	var env struct {
		Data pushResult `json:"data"`
	}
	if jErr := json.Unmarshal(buf.Bytes(), &env); jErr != nil {
		t.Fatalf("decode: %v\n%s", jErr, buf.String())
	}
	if env.Data.Deferred != 1 {
		t.Errorf("deferred = %d, want 1", env.Data.Deferred)
	}
	fake.mu.Lock()
	puts := len(fake.puts)
	fake.mu.Unlock()
	if puts != 0 {
		t.Errorf("expected no PUT for a deferred routine, got %d", puts)
	}
}

// TestPushDryRunWritesNothing proves --dry-run plans without writing.
func TestPushDryRunWritesNothing(t *testing.T) {
	fake := newRWAtelier(
		map[string][]string{"A.mac": {"A ;v1", " q"}},
		map[string]string{"A.mac": "2026-05-20 09:14:22.000"},
	)
	srv := fake.start()
	defer srv.Close()

	conn := pullThenConn(t, srv)
	if err := os.WriteFile(conn.Layout().RoutinePath("A.mac"), []byte("A ;mine\n q\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	conn.DryRun = true

	cc, buf := jsonCtx()
	if err := (&pushCmd{}).Run(cc, conn); err != nil {
		t.Fatalf("dry-run push: %v", err)
	}
	var env struct {
		Data pushResult `json:"data"`
	}
	if jErr := json.Unmarshal(buf.Bytes(), &env); jErr != nil {
		t.Fatalf("decode: %v\n%s", jErr, buf.String())
	}
	if env.Data.Pushed != 0 || !env.Data.DryRun {
		t.Errorf("dry run pushed=%d dryRun=%v, want 0/true", env.Data.Pushed, env.Data.DryRun)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.puts) != 0 || len(fake.compiled) != 0 {
		t.Errorf("dry run must not write: puts=%v compiled=%v", fake.puts, fake.compiled)
	}
}

// TestPushCompileErrorSavesButFlags proves that a compile failure after a
// successful PUT is a finding (exit 3), not a refusal (exit 4): the source is
// written (server reflects it) and the manifest is updated, but the result is
// flagged.
func TestPushCompileErrorSavesButFlags(t *testing.T) {
	fake := newRWAtelier(
		map[string][]string{"A.mac": {"A ;v1", " q"}},
		map[string]string{"A.mac": "2026-05-20 09:14:22.000"},
	)
	fake.compileErr = "ERROR: A.mac line 2: SYNTAX"
	srv := fake.start()
	defer srv.Close()

	conn := pullThenConn(t, srv)
	if err := os.WriteFile(conn.Layout().RoutinePath("A.mac"), []byte("A ;mine\n bad syntax here\n q\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cc, buf := jsonCtx()
	err := (&pushCmd{}).Run(cc, conn)
	if code := exitOf(t, err); code != clikit.ExitCheck {
		t.Fatalf("compile-error push exit = %d, want %d (finding, not refusal)", code, clikit.ExitCheck)
	}
	var env struct {
		Data pushResult `json:"data"`
	}
	if jErr := json.Unmarshal(buf.Bytes(), &env); jErr != nil {
		t.Fatalf("decode: %v\n%s", jErr, buf.String())
	}
	if env.Data.Pushed != 1 || env.Data.CompileErrors == 0 {
		t.Errorf("pushed=%d compileErrors=%d, want pushed=1 and >0 compile errors", env.Data.Pushed, env.Data.CompileErrors)
	}
	// The write succeeded: the server holds the new source.
	fake.mu.Lock()
	got := strings.Join(fake.content["A.mac"], "\n")
	fake.mu.Unlock()
	if got != "A ;mine\n bad syntax here\n q" {
		t.Errorf("server content = %q, want the written (uncompilable) source saved", got)
	}
}

// TestPushNoManifestRefuses proves push requires a pulled mirror (its
// conflict-check basis).
func TestPushNoManifestRefuses(t *testing.T) {
	conn := &config.Conn{
		BaseURL: "https://h:52773/api/atelier/v1/", Instance: "i", Namespace: "VISTA",
		Mirror: t.TempDir(), Concurrency: 2,
	}
	cc, _ := jsonCtx()
	err := (&pushCmd{}).Run(cc, conn)
	if code := exitOf(t, err); code != clikit.ExitRuntime {
		t.Fatalf("push without manifest exit = %d, want %d", code, clikit.ExitRuntime)
	}
}

// TestPushLockBlocksConcurrent proves the single-writer lock: a second push
// fails with exit 4 while a lock is held against the same mirror/namespace.
func TestPushLockBlocksConcurrent(t *testing.T) {
	fake := newRWAtelier(
		map[string][]string{"A.mac": {"A ;v1", " q"}},
		map[string]string{"A.mac": "2026-05-20 09:14:22.000"},
	)
	srv := fake.start()
	defer srv.Close()
	conn := pullThenConn(t, srv)
	if err := os.WriteFile(conn.Layout().RoutinePath("A.mac"), []byte("A ;mine\n q\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Pre-create the lock file as a live holder (this PID) so Acquire refuses.
	lockPath := conn.Layout().PushLockPath()
	if err := os.WriteFile(lockPath, []byte(fmt.Sprintf(`{"host":%q,"pid":%d,"startedAt":"2999-01-01T00:00:00Z"}`, hostname(), os.Getpid())), 0o644); err != nil {
		t.Fatal(err)
	}

	cc, _ := jsonCtx()
	err := (&pushCmd{}).Run(cc, conn)
	if code := exitOf(t, err); code != clikit.ExitRefused {
		t.Fatalf("push with held lock exit = %d, want %d", code, clikit.ExitRefused)
	}
	var e *clikit.Error
	if !errors.As(err, &e) || e.Code != "LOCK_HELD" {
		t.Errorf("expected LOCK_HELD error, got %+v", err)
	}
}

func hostname() string {
	h, _ := os.Hostname()
	return h
}
