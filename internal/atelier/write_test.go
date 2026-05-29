package atelier

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPutDoc(t *testing.T) {
	var gotMethod, gotPath, gotCT string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotCT = r.Method, r.URL.Path, r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		// Atelier echoes the stored doc (with the new server timestamp) in result.
		_, _ = io.WriteString(w, `{"status":{"errors":[]},"result":{
			"name":"DGREG.mac","ts":"2026-05-28 12:00:00.000","cat":"RTN","content":["DGREG ;reg"," q"]}}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	res, err := c.PutDoc(context.Background(), "DGREG.mac", []string{"DGREG ;reg", " q"})
	if err != nil {
		t.Fatalf("PutDoc: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method = %q, want PUT", gotMethod)
	}
	if gotPath != "/api/atelier/v1/VISTA/doc/DGREG.mac" {
		t.Errorf("path = %q", gotPath)
	}
	if !strings.Contains(gotCT, "application/json") {
		t.Errorf("content-type = %q", gotCT)
	}
	// Body must be the Atelier doc shape: {enc:false, content:[...]}.
	var sent struct {
		Enc     bool     `json:"enc"`
		Content []string `json:"content"`
	}
	if err := json.Unmarshal(gotBody, &sent); err != nil {
		t.Fatalf("decode sent body: %v\n%s", err, gotBody)
	}
	if sent.Enc {
		t.Error("PUT body enc should be false")
	}
	if len(sent.Content) != 2 || sent.Content[0] != "DGREG ;reg" {
		t.Errorf("sent content = %v", sent.Content)
	}
	if res.TS != "2026-05-28 12:00:00.000" {
		t.Errorf("returned server TS = %q", res.TS)
	}
}

func TestPutDocPercentEscaped(t *testing.T) {
	var gotRaw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRaw = r.URL.EscapedPath()
		_, _ = io.WriteString(w, `{"status":{"errors":[]},"result":{"name":"%ZV.mac","ts":"t"}}`)
	}))
	defer srv.Close()
	if _, err := newTestClient(t, srv).PutDoc(context.Background(), "%ZV.mac", []string{"x"}); err != nil {
		t.Fatalf("PutDoc: %v", err)
	}
	if !strings.Contains(gotRaw, "%25ZV.mac") {
		t.Errorf("expected percent-encoded name, raw path = %q", gotRaw)
	}
}

func TestPutDocServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"status":{"errors":[{"error":"write failed","code":"5002"}]}}`)
	}))
	defer srv.Close()
	_, err := newTestClient(t, srv).PutDoc(context.Background(), "A.mac", []string{"x"})
	if err == nil || !strings.Contains(err.Error(), "write failed") {
		t.Fatalf("expected server error surfaced, got %v", err)
	}
}

func TestStat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("Stat should GET, got %s", r.Method)
		}
		if r.URL.Path != "/api/atelier/v1/VISTA/doc/DGREG.mac" {
			t.Errorf("path = %q", r.URL.Path)
		}
		// Atelier supports a HEAD-like cheap fetch; we GET and read ts/enc.
		_, _ = io.WriteString(w, `{"status":{"errors":[]},"result":{"name":"DGREG.mac","ts":"2026-05-20 09:14:22.000","enc":false,"content":["x"]}}`)
	}))
	defer srv.Close()
	st, ok, err := newTestClient(t, srv).Stat(context.Background(), "DGREG.mac")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !ok {
		t.Fatal("expected doc to exist")
	}
	if st.TS != "2026-05-20 09:14:22.000" {
		t.Errorf("Stat TS = %q", st.TS)
	}
}

func TestStatMissingReturnsNotOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"status":{"errors":[{"error":"ERROR #5002: doc 'X.mac' does not exist","code":"5002"}]}}`)
	}))
	defer srv.Close()
	st, ok, err := newTestClient(t, srv).Stat(context.Background(), "X.mac")
	if err != nil {
		t.Fatalf("Stat on missing should not error, got %v", err)
	}
	if ok {
		t.Errorf("expected ok=false for a missing doc, got %+v", st)
	}
}

func TestCompile(t *testing.T) {
	var gotMethod, gotPath, gotQuery string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		gotBody, _ = io.ReadAll(r.Body)
		_, _ = io.WriteString(w, `{"status":{"errors":[]},"console":["Compilation started","Compilation finished successfully"],
			"result":{"content":[{"name":"DGREG.mac","status":"","ts":"2026-05-28 12:00:01.000"}]}}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	res, err := c.Compile(context.Background(), []string{"DGREG.mac"}, "cuk")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/api/atelier/v1/VISTA/action/compile" {
		t.Errorf("path = %q", gotPath)
	}
	if !strings.Contains(gotQuery, "flags=cuk") {
		t.Errorf("query = %q, want flags=cuk", gotQuery)
	}
	var names []string
	if err := json.Unmarshal(gotBody, &names); err != nil {
		t.Fatalf("compile body should be a JSON array of names: %v\n%s", err, gotBody)
	}
	if len(names) != 1 || names[0] != "DGREG.mac" {
		t.Errorf("compile body = %v", names)
	}
	if !res.OK() {
		t.Errorf("expected successful compile, diagnostics: %v", res.Diagnostics)
	}
}

func TestCompileReportsErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Compile failure: status.errors carries the diagnostic; per-doc status
		// also flags it. The HTTP call itself succeeds.
		_, _ = io.WriteString(w, `{"status":{"errors":[{"error":"ERROR: DGREG.mac line 3: undefined label","code":"compile"}]},
			"result":{"content":[{"name":"DGREG.mac","status":"ERROR: DGREG.mac line 3: undefined label"}]}}`)
	}))
	defer srv.Close()
	res, err := newTestClient(t, srv).Compile(context.Background(), []string{"DGREG.mac"}, "cuk")
	if err != nil {
		t.Fatalf("Compile transport should succeed even on compile errors, got %v", err)
	}
	if res.OK() {
		t.Error("expected OK()=false on a compile error")
	}
	if len(res.Diagnostics) == 0 || !strings.Contains(res.Diagnostics[0], "undefined label") {
		t.Errorf("expected compile diagnostic, got %v", res.Diagnostics)
	}
}
