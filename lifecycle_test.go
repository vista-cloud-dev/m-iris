package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vista-cloud-dev/m-iris/clikit"
	"github.com/vista-cloud-dev/m-iris/internal/config"
)

// rootServer serves the Atelier root descriptor (the health/status substrate).
func rootServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":{"errors":[]},"result":{"content":{` +
			`"version":"IRIS for UNIX 2024.1","api":7,"namespaces":["%SYS","USER","VISTA"]}}}`))
	}))
}

func remoteConn(baseURL string) *config.Conn {
	return &config.Conn{Transport: "remote", BaseURL: baseURL + "/api/atelier/v1/", Namespace: "VISTA"}
}

// TestLifecycleStatus_RemoteHealthy reports running/healthy + version +
// namespaces from the Atelier root on the remote (attach) transport.
func TestLifecycleStatus_RemoteHealthy(t *testing.T) {
	srv := rootServer()
	defer srv.Close()

	cc, buf := jsonCtx()
	if err := (lifeStatusCmd{}).Run(cc, remoteConn(srv.URL)); err != nil {
		t.Fatalf("status: %v", err)
	}
	var env struct {
		Data lifecycleStatus `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v\n%s", err, buf.String())
	}
	d := env.Data
	if !d.Running || !d.Healthy || d.Version != "IRIS for UNIX 2024.1" {
		t.Errorf("status = %+v, want running+healthy with version", d)
	}
	if len(d.Namespaces) != 3 {
		t.Errorf("namespaces = %v, want 3", d.Namespaces)
	}
}

// TestLifecycleProbe_ExitCodes: --probe is the CI gate — exit 0 healthy, exit 6
// unreachable. The envelope is emitted either way.
func TestLifecycleProbe_ExitCodes(t *testing.T) {
	srv := rootServer()
	cc, _ := jsonCtx()
	if err := (lifeStatusCmd{Probe: true}).Run(cc, remoteConn(srv.URL)); err != nil {
		t.Fatalf("healthy probe should exit 0: %v", err)
	}
	srv.Close() // now unreachable

	cc, _ = jsonCtx()
	err := (lifeStatusCmd{Probe: true}).Run(cc, remoteConn(srv.URL))
	if code := exitOf(t, err); code != clikit.ExitUnreachable {
		t.Fatalf("unreachable probe exit = %d, want %d", code, clikit.ExitUnreachable)
	}
}

// TestLifecycleProvision_UnsupportedOnRemote: you cannot create a namespace over
// Atelier (risk B4) — provision/destroy must report unsupported (exit 7) so
// conformance runs in attached mode there.
func TestLifecycleProvision_UnsupportedOnRemote(t *testing.T) {
	cc, _ := jsonCtx()
	err := (lifeProvisionCmd{}).Run(cc, remoteConn("http://unused"))
	if code := exitOf(t, err); code != clikit.ExitUnsupported {
		t.Fatalf("provision on remote exit = %d, want %d (unsupported)", code, clikit.ExitUnsupported)
	}
}

// TestLifecycleWait_BecomesHealthy returns once the root probe is healthy.
func TestLifecycleWait_BecomesHealthy(t *testing.T) {
	srv := rootServer()
	defer srv.Close()
	cc, _ := jsonCtx()
	if err := (&lifeWaitCmd{Timeout: 2}).Run(cc, remoteConn(srv.URL)); err != nil {
		t.Fatalf("wait on a healthy engine: %v", err)
	}
}

// TestLifecycleWait_TimesOut exits 6 when the engine never becomes healthy.
func TestLifecycleWait_TimesOut(t *testing.T) {
	srv := rootServer()
	srv.Close() // never reachable
	cc, _ := jsonCtx()
	err := (&lifeWaitCmd{Timeout: 1}).Run(cc, remoteConn(srv.URL))
	if code := exitOf(t, err); code != clikit.ExitUnreachable {
		t.Fatalf("wait timeout exit = %d, want %d", code, clikit.ExitUnreachable)
	}
}
