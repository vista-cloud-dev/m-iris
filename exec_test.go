package main

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vista-cloud-dev/m-iris/internal/config"
	"github.com/vista-cloud-dev/m-iris/internal/driver"
)

// fakeRunnerAtelier serves the slice of Atelier the remote exec path needs: it
// accepts PUT/Compile (runner + IO helper + staged routines) and answers
// action/query by modeling the m_iris.* runner procedures against an in-memory
// result global. It is enough to drive exec load/run/eval end to end without a
// live IRIS (the real-engine tier covers the ObjectScript itself).
func fakeRunnerAtelier(t *testing.T) *httptest.Server {
	t.Helper()
	globals := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/doc/"):
			name := r.URL.Path[strings.Index(r.URL.Path, "/doc/")+len("/doc/"):]
			_, _ = io.WriteString(w, `{"status":{"errors":[]},"result":{"name":"`+name+`","ts":"2026-06-12 00:00:00.000","status":"","content":[]}}`)
		case strings.Contains(r.URL.Path, "/action/compile"):
			_, _ = io.WriteString(w, `{"status":{"errors":[]},"result":{"content":[]}}`)
		case strings.Contains(r.URL.Path, "/action/query"):
			body, _ := io.ReadAll(r.Body)
			var q struct {
				Query      string   `json:"query"`
				Parameters []string `json:"parameters"`
			}
			_ = json.Unmarshal(body, &q)
			_, _ = io.WriteString(w, `{"status":{"errors":[]},"result":{"content":[`+answerQuery(q.Query, q.Parameters, globals)+`]}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// answerQuery models the m_iris.* SqlProcs the remote transport calls.
func answerQuery(sql string, params []string, globals map[string]string) string {
	switch {
	case strings.Contains(sql, "RunRef"):
		rid := params[0]
		// A clean run: status 0, done 1; the runner would have captured the
		// entryref's device output into ^mIrisRun(rid,"out") — model "ran\n".
		globals[`^mIrisRun("`+rid+`","out")`] = "ran\n"
		globals[`^mIrisRun("`+rid+`","status")`] = "0"
		globals[`^mIrisRun("`+rid+`","done")`] = "1"
		return `{"status":"0"}`
	case strings.Contains(sql, "Eval"):
		rid := params[0]
		globals[`^mIrisRun("`+rid+`","status")`] = "0"
		globals[`^mIrisRun("`+rid+`","done")`] = "1"
		return `{"status":"0"}`
	case strings.Contains(sql, "GetOut"):
		rid := params[0]
		enc := base64.StdEncoding.EncodeToString([]byte(globals[`^mIrisRun("`+rid+`","out")`]))
		return `{"out":` + jsonStr(enc) + `}`
	case strings.Contains(sql, "GetGlobal"):
		return `{"value":` + jsonStr(globals[params[0]]) + `}`
	case strings.Contains(sql, "SELECT 1"):
		return `{"one":"1"}`
	}
	return `{}`
}

func jsonStr(s string) string { b, _ := json.Marshal(s); return string(b) }

func execConn(base string) *config.Conn {
	return &config.Conn{Transport: "remote", BaseURL: base + "/api/atelier/v1/", Namespace: "VISTA", User: "_SYSTEM", Password: "x"}
}

// TestExecLoad_StagesDotMAsInt drives `exec load` over the fake Atelier and
// asserts the neutral .m source is staged under a .int docname (the SDK Client
// → driver seam v-pkg's install path rides).
func TestExecLoad_StagesDotMAsInt(t *testing.T) {
	srv := fakeRunnerAtelier(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "ZVPKGINS.m")
	if err := os.WriteFile(path, []byte("ZVPKGINS ;gen\nEN ;\n W \"hi\",!\n Q\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cc, buf := jsonCtx()
	if err := (&execLoadCmd{Paths: []string{path}}).Run(cc, execConn(srv.URL)); err != nil {
		t.Fatalf("exec load: %v", err)
	}
	var env struct {
		OK   bool           `json:"ok"`
		Data execLoadResult `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v\n%s", err, buf.String())
	}
	if !env.OK || len(env.Data.Loaded) != 1 || env.Data.Loaded[0] != "ZVPKGINS.int" {
		t.Errorf("load = %+v, want one ZVPKGINS.int", env.Data)
	}
}

// TestExecRun_SurfacesStdout drives `exec run` and asserts the captured device
// output flows back as ExecResult.Stdout (the marker channel v-pkg parses).
func TestExecRun_SurfacesStdout(t *testing.T) {
	srv := fakeRunnerAtelier(t)
	cc, buf := jsonCtx()
	if err := (&execRunCmd{EntryRef: "EN^ZVPKGINS"}).Run(cc, execConn(srv.URL)); err != nil {
		t.Fatalf("exec run: %v", err)
	}
	var env struct {
		Data execResult `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v\n%s", err, buf.String())
	}
	if env.Data.Stdout != "ran\n" {
		t.Errorf("stdout = %q, want \"ran\\n\"", env.Data.Stdout)
	}
}

// TestExecEval_Runs drives `exec eval` (a single command) over the fake.
func TestExecEval_Runs(t *testing.T) {
	srv := fakeRunnerAtelier(t)
	cc, buf := jsonCtx()
	if err := (&execEvalCmd{Command: []string{"set", "^x=1"}}).Run(cc, execConn(srv.URL)); err != nil {
		t.Fatalf("exec eval: %v", err)
	}
	if !strings.Contains(buf.String(), `"status": 0`) {
		t.Errorf("eval envelope = %s", buf.String())
	}
}

// TestExecAxis_Advertised proves caps honestly advertises the exec axis and the
// CLI mounts it (conformance asserts advertised == implemented).
func TestExecAxis_Advertised(t *testing.T) {
	if len(driver.CapsDoc().Axes.Exec) == 0 {
		t.Error("caps does not advertise the exec axis")
	}
	if _, ok := any(CLI{}.Exec).(execCmd); !ok {
		t.Error("CLI has no exec axis")
	}
}
