package driver

import (
	"context"
	"testing"

	"github.com/vista-cloud-dev/m-iris/clikit"
)

// TestFakeTransport_RecordsAndScripts verifies the fake satisfies Transport,
// records calls in order, and returns scripted results — the substrate every
// unit test injects in place of a real IRIS.
func TestFakeTransport_RecordsAndScripts(t *testing.T) {
	ctx := context.Background()
	ft := &FakeTransport{
		ExecFn: func(_ context.Context, req ExecRequest) (ExecResult, error) {
			if req.EntryRef == "BROKEN^X" {
				return ExecResult{
					EngineError: &clikit.EngineError{
						Routine: "X", Line: 3, Mnemonic: "<NOROUTINE>", Text: "no such routine",
					},
				}, nil
			}
			return ExecResult{Stdout: "ok", Status: 0}, nil
		},
	}

	// A clean run returns canned stdout.
	res, err := ft.Exec(ctx, ExecRequest{EntryRef: "RUN^STDHARN"})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if res.Stdout != "ok" || res.Status != 0 {
		t.Errorf("clean run = %+v, want stdout=ok status=0", res)
	}

	// A fault is data, not a Go error: EngineError is populated, err is nil.
	res, err = ft.Exec(ctx, ExecRequest{EntryRef: "BROKEN^X"})
	if err != nil {
		t.Fatalf("fault should be data, not error: %v", err)
	}
	if res.EngineError == nil || res.EngineError.Mnemonic != "<NOROUTINE>" {
		t.Errorf("EngineError = %+v, want <NOROUTINE>", res.EngineError)
	}

	// Unset verbs are safe zero-value no-ops and still record.
	if _, err := ft.Health(ctx); err != nil {
		t.Fatalf("Health: %v", err)
	}

	wantVerbs := []string{"Exec", "Exec", "Health"}
	if len(ft.Calls) != len(wantVerbs) {
		t.Fatalf("recorded %d calls, want %d", len(ft.Calls), len(wantVerbs))
	}
	for i, v := range wantVerbs {
		if ft.Calls[i].Verb != v {
			t.Errorf("call %d verb = %q, want %q", i, ft.Calls[i].Verb, v)
		}
	}
}
