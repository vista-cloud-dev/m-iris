package atelier

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestServerInfo_RoundTrip drives the Atelier root probe, the foundation of
// lifecycle status / health / doctor: it returns the engine version + the
// namespaces the credential can see. The probe targets the UNVERSIONED root
// (/api/atelier/) — the version-prefixed root 404s on modern IRIS (validated
// against IRIS 2026.1).
func TestServerInfo_RoundTrip(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":{"errors":[]},"result":{"content":{` +
			`"version":"IRIS for UNIX (Ubuntu Server LTS) 2024.1","api":7,` +
			`"namespaces":["%SYS","USER","VISTA"]}}}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL + "/api/atelier/v1/", Namespace: "USER"})
	info, err := c.ServerInfo(context.Background())
	if err != nil {
		t.Fatalf("ServerInfo: %v", err)
	}
	if gotPath != "/api/atelier/" {
		t.Errorf("path = %q, want /api/atelier/ (unversioned root)", gotPath)
	}
	if info.Version != "IRIS for UNIX (Ubuntu Server LTS) 2024.1" || info.API != 7 {
		t.Errorf("info = %+v", info)
	}
	if len(info.Namespaces) != 3 || info.Namespaces[2] != "VISTA" {
		t.Errorf("namespaces = %v", info.Namespaces)
	}
}

// TestServerInfo_AuthDistinct maps 401 and 403 to distinct typed errors so
// doctor can tell "bad credentials" from "no privilege" (risks C3, C7).
func TestServerInfo_AuthDistinct(t *testing.T) {
	for _, tc := range []struct {
		code                   int
		wantUnauth, wantForbid bool
	}{
		{http.StatusUnauthorized, true, false},
		{http.StatusForbidden, false, true},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.code)
		}))
		c, _ := New(Config{BaseURL: srv.URL + "/api/atelier/v1/", Namespace: "USER"})
		_, err := c.ServerInfo(context.Background())
		srv.Close()
		if err == nil {
			t.Fatalf("HTTP %d: expected an error", tc.code)
		}
		if IsUnauthorized(err) != tc.wantUnauth {
			t.Errorf("HTTP %d: IsUnauthorized = %v, want %v", tc.code, IsUnauthorized(err), tc.wantUnauth)
		}
		if IsForbidden(err) != tc.wantForbid {
			t.Errorf("HTTP %d: IsForbidden = %v, want %v", tc.code, IsForbidden(err), tc.wantForbid)
		}
	}
}
