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

// TestQuery_RoundTrip drives action/query (the SQL endpoint that is the ENTIRE
// remote ObjectScript substrate — Atelier has no raw "run M" endpoint, so all
// remote exec/data/cover ride a SQL-invokable runner called through here). It
// asserts the request shape (POST {ns}/action/query, {query,parameters}) and
// that the result.content row set is decoded.
func TestQuery_RoundTrip(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":{"errors":[]},"console":[],"result":{"content":[`+
			`{"status":"0","error":""}`+
			`]}}`)
	}))
	defer srv.Close()

	c, err := New(Config{BaseURL: srv.URL + "/api/atelier/v1/", Namespace: "USER"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rows, err := c.Query(context.Background(),
		"SELECT m_iris.RunRef(?,?,?) AS status", "rid1", "RUN^STDHARN", "")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/USER/action/query") {
		t.Errorf("path = %q, want …/USER/action/query", gotPath)
	}
	var req struct {
		Query      string   `json:"query"`
		Parameters []string `json:"parameters"`
	}
	if err := json.Unmarshal([]byte(gotBody), &req); err != nil {
		t.Fatalf("decode request body %q: %v", gotBody, err)
	}
	if !strings.Contains(req.Query, "RunRef") || len(req.Parameters) != 3 {
		t.Errorf("request = %+v, want RunRef + 3 params", req)
	}
	if len(rows) != 1 || rows[0]["status"] != "0" {
		t.Errorf("rows = %v, want one row with status=0", rows)
	}
}

// TestQuery_ServerError surfaces an Atelier-side SQL error as a Go error (e.g. a
// privilege failure on the runner procedure — risk C7).
func TestQuery_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"status":{"errors":[{"error":"[SQLCODE: <-99>] privilege failure","code":"99"}]},"result":{}}`)
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL + "/api/atelier/v1/", Namespace: "USER"})
	if _, err := c.Query(context.Background(), "SELECT 1"); err == nil {
		t.Fatal("expected a Go error for a server-side SQL failure")
	}
}
