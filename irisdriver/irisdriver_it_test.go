package irisdriver

import (
	"context"
	"os"
	"strings"
	"testing"

	mdriver "github.com/vista-cloud-dev/m-driver-sdk"
)

// TestRealEngine_FacadeHealthAndZV exercises the public facade end-to-end against
// a live IRIS: New → Health (privileged SELECT 1) → Exec `W $ZV` (the T0.1
// readiness gate). Gated by M_IRIS_IT=1 so unit CI skips it. Example:
//
//	M_IRIS_IT=1 M_IRIS_BASE_URL=http://localhost:52774/api/atelier/v1/ \
//	M_IRIS_NAMESPACE=USER M_IRIS_USER=_SYSTEM M_IRIS_PASSWORD=testsys \
//	go test -run RealEngine ./irisdriver/ -v
func TestRealEngine_FacadeHealthAndZV(t *testing.T) {
	if os.Getenv("M_IRIS_IT") != "1" {
		t.Skip("set M_IRIS_IT=1 (+ M_IRIS_* connection env) to run the real-engine facade check")
	}
	env := func(k, d string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		return d
	}
	tr, err := New(Config{
		BaseURL:   env("M_IRIS_BASE_URL", "http://localhost:52773/api/atelier/v1/"),
		Namespace: env("M_IRIS_NAMESPACE", "USER"),
		User:      env("M_IRIS_USER", "_SYSTEM"),
		Password:  env("M_IRIS_PASSWORD", "SYS"),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()

	h, err := tr.Health(ctx)
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if !h.Healthy {
		t.Fatalf("Health not healthy: %+v", h)
	}

	// IRIS Exec captures the result-global, NOT device `W` output (the runner
	// xecutes the command with no IO redirection), so the command writes $zv into
	// ^mIrisRun(rid,"out"), which remote.Exec returns as Stdout. (On YottaDB the
	// session's device stdout is captured directly — a real cross-engine
	// difference VistaEngine handles via Health.Version rather than Exec("W $ZV").)
	res, err := tr.Exec(ctx, mdriver.ExecRequest{
		Command: `set ^mIrisRun("zzv","out")=$zv`, Prefix: "zzv",
	})
	if err != nil {
		t.Fatalf("Exec($zv): %v", err)
	}
	if res.EngineError != nil {
		t.Fatalf("engineError: %+v", res.EngineError)
	}
	if !strings.Contains(res.Stdout, "IRIS") && !strings.Contains(res.Stdout, "Cache") {
		t.Fatalf("$ZV = %q, want an IRIS version banner", res.Stdout)
	}
	t.Logf("facade $ZV via result-global: %s", strings.TrimSpace(res.Stdout))
}
