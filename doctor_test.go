package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vista-cloud-dev/m-iris/clikit"
	"github.com/vista-cloud-dev/m-iris/internal/config"
)

// doctorServer is a configurable fake Atelier for the doctor matrix: it can
// force an auth code on the root, and serves action/query (SELECT 1) for the
// privilege probe.
func doctorServer(rootCode int, namespaces []string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/action/query") {
			_, _ = w.Write([]byte(`{"status":{"errors":[]},"result":{"content":[{"one":"1"}]}}`))
			return
		}
		// root descriptor
		if rootCode != 0 {
			w.WriteHeader(rootCode)
			return
		}
		nsJSON, _ := json.Marshal(namespaces)
		_, _ = w.Write([]byte(`{"status":{"errors":[]},"result":{"content":{` +
			`"version":"IRIS for UNIX 2024.1","api":7,"namespaces":` + string(nsJSON) + `}}}`))
	}))
}

func doctorConn(baseURL, ns string) *config.Conn {
	return &config.Conn{Transport: "remote", BaseURL: baseURL + "/api/atelier/v1/", Namespace: ns}
}

func decodeDoctor(t *testing.T, b []byte) doctorResult {
	t.Helper()
	var env struct {
		Data doctorResult `json:"data"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		t.Fatalf("decode doctor: %v\n%s", err, b)
	}
	return env.Data
}

func checkByName(d doctorResult, name string) (doctorCheck, bool) {
	for _, c := range d.Checks {
		if c.Name == name {
			return c, true
		}
	}
	return doctorCheck{}, false
}

// TestDoctor_AllGreen: a healthy reachable engine with the namespace present and
// SQL privilege → every check ok, exit 0.
func TestDoctor_AllGreen(t *testing.T) {
	srv := doctorServer(0, []string{"%SYS", "USER", "VISTA"})
	defer srv.Close()
	cc, buf := jsonCtx()
	if err := (doctorCmd{}).Run(cc, doctorConn(srv.URL, "VISTA")); err != nil {
		t.Fatalf("healthy doctor should exit 0: %v", err)
	}
	d := decodeDoctor(t, buf.Bytes())
	if !d.OK {
		t.Errorf("doctor.OK = false, want true: %+v", d.Checks)
	}
	for _, c := range d.Checks {
		if !c.OK {
			t.Errorf("check %q failed unexpectedly: %s", c.Name, c.Detail)
		}
	}
}

// TestDoctor_AuthFailExit5: reachable but 401 → reachable ok, auth fails, exit 5.
func TestDoctor_AuthFailExit5(t *testing.T) {
	srv := doctorServer(http.StatusUnauthorized, nil)
	defer srv.Close()
	cc, buf := jsonCtx()
	if err := (doctorCmd{}).Run(cc, doctorConn(srv.URL, "VISTA")); err != nil {
		t.Fatalf("doctor should not return an error (the data envelope carries the outcome): %v", err)
	}
	if code := cc.ExitCode(); code != clikit.ExitRuntime {
		t.Fatalf("auth-fail doctor exit = %d, want %d", code, clikit.ExitRuntime)
	}
	d := decodeDoctor(t, buf.Bytes())
	if c, _ := checkByName(d, "auth"); c.OK {
		t.Error("auth check should fail on 401")
	}
	if c, _ := checkByName(d, "reachable"); !c.OK {
		t.Error("reachable check should pass — the server answered")
	}
}

// TestDoctor_UnreachableExit6: connection refused → reachable fails, exit 6.
func TestDoctor_UnreachableExit6(t *testing.T) {
	srv := doctorServer(0, nil)
	srv.Close() // refuse connections
	cc, _ := jsonCtx()
	if err := (doctorCmd{}).Run(cc, doctorConn(srv.URL, "VISTA")); err != nil {
		t.Fatalf("doctor should not return an error: %v", err)
	}
	if code := cc.ExitCode(); code != clikit.ExitUnreachable {
		t.Fatalf("unreachable doctor exit = %d, want %d", code, clikit.ExitUnreachable)
	}
}

// TestDoctor_NamespaceMissingExit5: reachable+auth ok but the target namespace is
// absent → namespace check fails, exit 5.
func TestDoctor_NamespaceMissingExit5(t *testing.T) {
	srv := doctorServer(0, []string{"%SYS", "USER"})
	defer srv.Close()
	cc, buf := jsonCtx()
	if err := (doctorCmd{}).Run(cc, doctorConn(srv.URL, "VISTA")); err != nil {
		t.Fatalf("doctor should not return an error: %v", err)
	}
	if code := cc.ExitCode(); code != clikit.ExitRuntime {
		t.Fatalf("missing-namespace doctor exit = %d, want %d", code, clikit.ExitRuntime)
	}
	d := decodeDoctor(t, buf.Bytes())
	if c, _ := checkByName(d, "namespace"); c.OK {
		t.Error("namespace check should fail when VISTA is absent")
	}
}
