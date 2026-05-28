package atelier

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c, err := New(Config{BaseURL: srv.URL + "/api/atelier/v1/", Namespace: "VISTA", User: "u", Password: "p"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestDocNames(t *testing.T) {
	var gotPath, gotQuery, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery, gotAuth = r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":{"errors":[],"summary":""},"console":[],
			"result":{"content":[
				{"name":"DGREG.mac","cat":"RTN","ts":"2026-05-20 09:14:22.000"},
				{"name":"XUSER.mac","cat":"RTN","ts":"2026-05-19 17:02:10.000"}
			]}}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	docs, err := c.DocNames(context.Background(), "")
	if err != nil {
		t.Fatalf("DocNames: %v", err)
	}
	if len(docs) != 2 || docs[0].Name != "DGREG.mac" || docs[1].TS != "2026-05-19 17:02:10.000" {
		t.Fatalf("unexpected docs: %+v", docs)
	}
	if gotPath != "/api/atelier/v1/VISTA/docnames/RTN/mac" {
		t.Errorf("path = %q", gotPath)
	}
	if !strings.Contains(gotQuery, "generated=0") {
		t.Errorf("query = %q, want generated=0", gotQuery)
	}
	if gotAuth == "" {
		t.Error("expected basic auth header")
	}
}

func TestDocNamesBareArray(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":{"errors":[]},"result":[{"name":"A.mac"}]}`))
	}))
	defer srv.Close()
	docs, err := newTestClient(t, srv).DocNames(context.Background(), "")
	if err != nil {
		t.Fatalf("DocNames: %v", err)
	}
	if len(docs) != 1 || docs[0].Name != "A.mac" {
		t.Fatalf("unexpected docs: %+v", docs)
	}
}

func TestGetDoc(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/atelier/v1/VISTA/doc/DGREG.mac" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"status":{"errors":[]},"result":{
			"name":"DGREG.mac","cat":"RTN","ts":"2026-05-20 09:14:22.000","enc":false,
			"content":["DGREG ;reg","  q"]}}`))
	}))
	defer srv.Close()

	doc, err := newTestClient(t, srv).GetDoc(context.Background(), "DGREG.mac")
	if err != nil {
		t.Fatalf("GetDoc: %v", err)
	}
	if doc.Name != "DGREG.mac" || len(doc.Content) != 2 || doc.Content[0] != "DGREG ;reg" {
		t.Fatalf("unexpected doc: %+v", doc)
	}
}

func TestGetDocPercentRoutineEscaped(t *testing.T) {
	var gotRawPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRawPath = r.URL.EscapedPath()
		_, _ = w.Write([]byte(`{"status":{"errors":[]},"result":{"name":"%ZV.mac","enc":false,"content":["x"]}}`))
	}))
	defer srv.Close()
	if _, err := newTestClient(t, srv).GetDoc(context.Background(), "%ZV.mac"); err != nil {
		t.Fatalf("GetDoc: %v", err)
	}
	if !strings.Contains(gotRawPath, "%25ZV.mac") {
		t.Errorf("expected %% to be percent-encoded, raw path = %q", gotRawPath)
	}
}

func TestServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"status":{"errors":[{"error":"namespace UNKNOWN does not exist","code":"5001"}]}}`))
	}))
	defer srv.Close()
	_, err := newTestClient(t, srv).DocNames(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("expected server error surfaced, got %v", err)
	}
}

func TestUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	_, err := newTestClient(t, srv).DocNames(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected 401 error, got %v", err)
	}
}

func TestNewValidatesConfig(t *testing.T) {
	if _, err := New(Config{Namespace: "X"}); err == nil {
		t.Error("expected error for missing base URL")
	}
	if _, err := New(Config{BaseURL: "https://h/", Namespace: "X", ClientCert: "only-cert"}); err == nil {
		t.Error("expected error for half a client cert pair")
	}
	if _, err := New(Config{BaseURL: "not a url", Namespace: "X"}); err == nil {
		t.Error("expected error for relative base URL")
	}
}
