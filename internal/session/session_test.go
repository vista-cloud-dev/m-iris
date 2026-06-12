package session

import (
	"context"
	"encoding/base64"
	"strconv"
	"strings"
	"testing"

	mdriver "github.com/vista-cloud-dev/m-driver-sdk"
)

// fakeRun records the argv + stdin of the last session invocation and returns a
// scripted CmdOutput, so tests assert both how the `iris session` command is
// built (docker wrapping, namespace) and how its noisy stdout is parsed —
// without a real engine.
type fakeRun struct {
	lastArgv  []string
	lastStdin string
	fn        func(stdin string) (CmdOutput, error)
}

func (f *fakeRun) run(_ context.Context, argv []string, stdin string) (CmdOutput, error) {
	f.lastArgv, f.lastStdin = argv, stdin
	if f.fn != nil {
		return f.fn(stdin)
	}
	return CmdOutput{}, nil
}

// sessionStdout reproduces the shape `iris session` emits: banner + prompt noise,
// then the captured region between the begin marker and the result marker, then
// the trailing prompt — so the parser is tested against realistic noise.
func sessionStdout(captured string, status int, frame string) string {
	return "\nNode: host, Instance: IRIS\n\nUSER>\n" +
		beginMark + "\n" + captured +
		endMark + strconv.Itoa(status) + "|" + frame + "\n\nUSER>\n"
}

func dockerCfg() Config {
	return Config{Transport: "docker", Container: "m-test-iris", Instance: "IRIS", Namespace: "USER"}
}

func TestExec_Eval_CapturesStdoutAndWrapsDocker(t *testing.T) {
	fr := &fakeRun{fn: func(string) (CmdOutput, error) {
		return CmdOutput{Stdout: sessionStdout("hi=42\n", 0, "")}, nil
	}}
	s := New(dockerCfg(), fr.run)

	res, err := s.Exec(context.Background(), mdriver.ExecRequest{Command: "write \"hi=\",6*7,!"})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if res.Status != 0 || res.EngineError != nil {
		t.Errorf("res = %+v, want status 0 no engineError", res)
	}
	if res.Stdout != "hi=42\n" {
		t.Errorf("Stdout = %q, want \"hi=42\\n\"", res.Stdout)
	}
	// docker wrapping + the iris session invocation into the right namespace.
	got := strings.Join(fr.lastArgv, " ")
	for _, want := range []string{"docker exec -i m-test-iris", "iris session IRIS -U USER"} {
		if !strings.Contains(got, want) {
			t.Errorf("argv %q missing %q", got, want)
		}
	}
	// The user command is carried as an escaped string literal and xecute'd.
	if !strings.Contains(fr.lastStdin, "xecute mcmd") {
		t.Errorf("stdin did not xecute the eval command:\n%s", fr.lastStdin)
	}
}

func TestExec_Fault_BecomesEngineError(t *testing.T) {
	fr := &fakeRun{fn: func(string) (CmdOutput, error) {
		return CmdOutput{Stdout: sessionStdout("", 5, "<UNDEFINED>|XLFISO|12|global undefined")}, nil
	}}
	s := New(dockerCfg(), fr.run)

	res, err := s.Exec(context.Background(), mdriver.ExecRequest{Command: "set x=^nope(1)"})
	if err != nil {
		t.Fatalf("a fault must be data, not a Go error: %v", err)
	}
	if res.EngineError == nil {
		t.Fatal("expected an EngineError")
	}
	if res.EngineError.Mnemonic != "<UNDEFINED>" || res.EngineError.Routine != "XLFISO" || res.EngineError.Line != 12 {
		t.Errorf("engineError = %+v, want <UNDEFINED> XLFISO:12", res.EngineError)
	}
}

func TestExec_Run_BuildsEntryRefDo(t *testing.T) {
	fr := &fakeRun{fn: func(string) (CmdOutput, error) {
		return CmdOutput{Stdout: sessionStdout("<<SX>>ok\n", 0, "")}, nil
	}}
	s := New(dockerCfg(), fr.run)
	if _, err := s.Exec(context.Background(), mdriver.ExecRequest{EntryRef: "EN^ZZSESX"}); err != nil {
		t.Fatalf("Exec run: %v", err)
	}
	if !strings.Contains(fr.lastStdin, `set mref="EN^ZZSESX"`) || !strings.Contains(fr.lastStdin, "do @mref") {
		t.Errorf("run stdin did not build the entryref do:\n%s", fr.lastStdin)
	}
}

func TestExec_Run_RecordsPidWhenPrefixed(t *testing.T) {
	fr := &fakeRun{fn: func(string) (CmdOutput, error) {
		return CmdOutput{Stdout: sessionStdout("", 0, "")}, nil
	}}
	s := New(dockerCfg(), fr.run)
	if _, err := s.Exec(context.Background(), mdriver.ExecRequest{EntryRef: "LOOP^ZZ", Prefix: "zzt9"}); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if !strings.Contains(fr.lastStdin, `^mIrisRun("zzt9","pid")=$job`) {
		t.Errorf("prefixed run did not register its pid:\n%s", fr.lastStdin)
	}
}

func TestHealth_ParsesVersion(t *testing.T) {
	fr := &fakeRun{fn: func(string) (CmdOutput, error) {
		return CmdOutput{Stdout: sessionStdout("IRIS for UNIX (Ubuntu) 2026.1 (Build 234U)", 0, "")}, nil
	}}
	s := New(dockerCfg(), fr.run)
	h, err := s.Health(context.Background())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if !h.Running || !h.Healthy {
		t.Errorf("health = %+v, want running+healthy", h)
	}
	if h.Version != "2026.1" {
		t.Errorf("version = %q, want 2026.1", h.Version)
	}
}

func TestSetGlobal_LocalNoDockerWrap(t *testing.T) {
	fr := &fakeRun{fn: func(string) (CmdOutput, error) {
		return CmdOutput{Stdout: sessionStdout("", 0, "")}, nil
	}}
	s := New(Config{Transport: "local", Instance: "IRIS", Namespace: "USER"}, fr.run)
	if err := s.SetGlobal(context.Background(), `^mFix("k")`, "hello"); err != nil {
		t.Fatalf("SetGlobal: %v", err)
	}
	if fr.lastArgv[0] != "iris" {
		t.Errorf("local argv should start with iris, got %v", fr.lastArgv)
	}
	if !strings.Contains(fr.lastStdin, `set @(`) {
		t.Errorf("SetGlobal stdin did not build an indirect set:\n%s", fr.lastStdin)
	}
}

func TestReadGlobal_DecodesBase64(t *testing.T) {
	val := "round\x01trip" // a control byte survives via base64
	enc := base64.StdEncoding.EncodeToString([]byte(val))
	fr := &fakeRun{fn: func(string) (CmdOutput, error) {
		return CmdOutput{Stdout: sessionStdout(enc, 0, "")}, nil
	}}
	s := New(dockerCfg(), fr.run)
	node, err := s.ReadGlobal(context.Background(), mdriver.GlobalRef{Ref: `^mFix("k")`})
	if err != nil {
		t.Fatalf("ReadGlobal: %v", err)
	}
	if node.Value != val {
		t.Errorf("value = %q, want %q", node.Value, val)
	}
}

func TestAbort_ReportsTerminatedPid(t *testing.T) {
	fr := &fakeRun{fn: func(string) (CmdOutput, error) {
		// The session abort eval writes the terminated pid into the captured region.
		return CmdOutput{Stdout: sessionStdout("173733", 0, "")}, nil
	}}
	s := New(dockerCfg(), fr.run)
	killed, err := s.Abort(context.Background(), "zzt9")
	if err != nil {
		t.Fatalf("Abort: %v", err)
	}
	if len(killed) != 1 || killed[0] != "173733" {
		t.Errorf("killed = %v, want [173733]", killed)
	}
}

func TestAbort_NothingLiveReturnsEmpty(t *testing.T) {
	fr := &fakeRun{fn: func(string) (CmdOutput, error) {
		return CmdOutput{Stdout: sessionStdout("", 0, "")}, nil
	}}
	s := New(dockerCfg(), fr.run)
	killed, err := s.Abort(context.Background(), "nothing")
	if err != nil {
		t.Fatalf("Abort: %v", err)
	}
	if len(killed) != 0 {
		t.Errorf("killed = %v, want none", killed)
	}
}

// satisfies the SDK Transport seam.
var _ mdriver.Transport = (*Session)(nil)
