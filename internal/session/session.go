// Package session is the IRIS `local` and `docker` transport: it drives an IRIS
// namespace by piping ObjectScript into an `iris session <instance> -U <ns>`
// process and capturing the principal device's output directly. Unlike the
// `remote` (Atelier) transport — which has no run endpoint and must route every
// operation through the m.iris.Runner SQL class and recover output from a result
// global — a session transport writes to stdout, so device `W` output is the
// command's output with no redirection machinery. The two transports differ only
// in reach: docker wraps the same `iris session` argv in `docker exec -i
// <container>`; local runs it on the host (iris on PATH).
package session

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	mdriver "github.com/vista-cloud-dev/m-driver-sdk"
)

// Session implements mdriver.Transport for local + docker, plus the driver-local
// Abort verb (exec.abort is not a neutral Transport method).
var _ mdriver.Transport = (*Session)(nil)

// Markers bracket the real result inside `iris session`'s noisy stdout (banner +
// `USER>` prompts). Everything between beginMark and endMark is the command's
// captured device output; endMark is followed by "<status>|<§7 frame>".
const (
	beginMark = "@@MIRIS-BEGIN@@"
	endMark   = "@@MIRIS-RESULT@@"
)

// Config is the resolved connection for a session transport. Container is used
// only by docker; Instance is the IRIS instance name inside the host/container
// (defaults to "IRIS"); Namespace is the `-U` target.
type Config struct {
	Transport string // "local" | "docker"
	Container string // docker: container name to exec into
	Instance  string // IRIS instance name (default "IRIS")
	Namespace string // -U namespace
}

// CmdOutput is one OS command's captured result (the in-strategy seam).
type CmdOutput struct {
	Stdout string
	Stderr string
	Code   int
}

// runFunc runs a prepared argv feeding stdin, returning captured output. It is
// the lower-level seam inside the session strategy; tests inject a fake to assert
// argv construction without a real engine, production uses osRun.
type runFunc func(ctx context.Context, argv []string, stdin string) (CmdOutput, error)

// Session is the local/docker Transport.
type Session struct {
	cfg Config
	run runFunc
}

// New builds a session transport. A nil run uses the real OS runner.
func New(cfg Config, run runFunc) *Session {
	if cfg.Instance == "" {
		cfg.Instance = "IRIS"
	}
	if run == nil {
		run = osRun
	}
	return &Session{cfg: cfg, run: run}
}

func (s *Session) isDocker() bool { return s.cfg.Transport == mdriver.TransportDocker }

// IsDocker reports whether this is the docker transport.
func (s *Session) IsDocker() bool { return s.isDocker() }

// Container is the docker container name (empty for local).
func (s *Session) Container() string { return s.cfg.Container }

// Docker runs a host `docker` command (start/stop/inspect) to manage the
// container itself — distinct from `docker exec`, which the verbs use to run
// inside it. Docker-transport only.
func (s *Session) Docker(ctx context.Context, args ...string) (CmdOutput, error) {
	return s.run(ctx, append([]string{"docker"}, args...), "")
}

// sessionArgv is the `iris session` invocation that reads ObjectScript on stdin.
func (s *Session) sessionArgv() []string {
	return []string{"iris", "session", s.cfg.Instance, "-U", s.cfg.Namespace}
}

// wrap adapts an argv to the active transport: `docker exec -i <container>` for
// docker, the bare argv for local.
func (s *Session) wrap(argv []string) []string {
	if s.isDocker() {
		return append([]string{"docker", "exec", "-i", s.cfg.Container}, argv...)
	}
	return argv
}

// irisString renders s as an ObjectScript string literal (doubling embedded
// quotes), so a user command/ref/value is carried safely into the session script.
func irisString(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// execScript builds the single-execution stdin for an ExecRequest. The user code
// runs inside a one-line TRY/CATCH (interactive `iris session` does NOT honor a
// $ZTRAP set on a prior stdin line — each line executes independently at the
// prompt — so the trap must be in the same line as the code). On a fault the
// catch emits status 5 + the §7 frame "mnemonic|routine|line|text".
func (s *Session) execScript(req mdriver.ExecRequest) (string, error) {
	var setup, inner string
	switch {
	case req.EntryRef != "":
		setup = "set mref=" + irisString(req.EntryRef) + "\n"
		inner = "do @mref"
	case req.Command != "":
		setup = "set mcmd=" + irisString(req.Command) + "\n"
		inner = "xecute mcmd"
	default:
		return "", fmt.Errorf("session: exec needs an entryref or a command")
	}
	if req.Prefix != "" {
		// Register this process so `exec abort --prefix` can stop the run in
		// flight; "done" is set after the run completes (see runScript on Abort).
		setup += "set ^mIrisRun(" + irisString(req.Prefix) + `,"pid")=$job` + "\n"
	}
	return setup + s.trapLine(inner), nil
}

// trapLine wraps inner in the begin/result-marker + one-line TRY/CATCH and halts.
func (s *Session) trapLine(inner string) string {
	return `write "` + beginMark + `",! set st=0,em="" ` +
		`try { ` + inner + ` } ` +
		`catch ex { set st=5,em=ex.Name_"|"_$piece(ex.Location,"^",2)_"|"_$piece($piece(ex.Location,"^",1),"+",2)_"|"_ex.DisplayString() } ` +
		`write "` + endMark + `",st,"|",em,! halt` + "\n"
}

// runScript pipes a prepared session script and parses the bracketed result.
func (s *Session) runScript(ctx context.Context, script string) (captured string, status int, eng *mdriver.EngineError, err error) {
	out, rerr := s.run(ctx, s.wrap(s.sessionArgv()), script)
	if rerr != nil {
		return "", 0, nil, rerr
	}
	captured, status, eng, ok := parseSession(out.Stdout)
	if !ok {
		return "", 0, nil, fmt.Errorf("session: could not parse iris session output (stderr: %q)", strings.TrimSpace(out.Stderr))
	}
	return captured, status, eng, nil
}

// parseSession extracts the captured output and "<status>|<frame>" tail from a
// session's stdout, discarding the surrounding banner/prompt noise.
func parseSession(stdout string) (captured string, status int, eng *mdriver.EngineError, ok bool) {
	bi := strings.Index(stdout, beginMark)
	if bi < 0 {
		return "", 0, nil, false
	}
	rest := stdout[bi+len(beginMark):]
	rest = strings.TrimPrefix(rest, "\r")
	rest = strings.TrimPrefix(rest, "\n")
	ki := strings.Index(rest, endMark)
	if ki < 0 {
		return "", 0, nil, false
	}
	captured = rest[:ki]
	line := rest[ki+len(endMark):]
	if nl := strings.IndexAny(line, "\r\n"); nl >= 0 {
		line = line[:nl]
	}
	stStr, frame := line, ""
	if p := strings.IndexByte(line, '|'); p >= 0 {
		stStr, frame = line[:p], line[p+1:]
	}
	status, _ = strconv.Atoi(strings.TrimSpace(stStr))
	if status == 5 {
		eng = parseFrame(frame)
	}
	return captured, status, eng, true
}

// parseFrame parses a "mnemonic|routine|line|text" §7 error frame.
func parseFrame(raw string) *mdriver.EngineError {
	parts := strings.SplitN(raw, "|", 4)
	eng := &mdriver.EngineError{}
	if len(parts) > 0 {
		eng.Mnemonic = parts[0]
	}
	if len(parts) > 1 {
		eng.Routine = parts[1]
	}
	if len(parts) > 2 {
		eng.Line, _ = strconv.Atoi(parts[2])
	}
	if len(parts) > 3 {
		eng.Text = parts[3]
	}
	return eng
}

// Exec runs an entryref or evaluates a command in the namespace, capturing device
// output. A fault is data (ExecResult.EngineError, §7), not a Go error.
func (s *Session) Exec(ctx context.Context, req mdriver.ExecRequest) (mdriver.ExecResult, error) {
	script, err := s.execScript(req)
	if err != nil {
		return mdriver.ExecResult{}, err
	}
	captured, status, eng, err := s.runScript(ctx, script)
	if err != nil {
		return mdriver.ExecResult{}, err
	}
	return mdriver.ExecResult{Stdout: captured, Status: status, EngineError: eng}, nil
}

var versionRe = regexp.MustCompile(`\d{4}\.\d+`)

// Health probes readiness + version in one round-trip: `write $zversion`. The
// session is healthy when it answers with a non-empty version banner.
func (s *Session) Health(ctx context.Context) (mdriver.Health, error) {
	captured, _, eng, err := s.runScript(ctx, s.trapLine("write $zversion"))
	if err != nil {
		return mdriver.Health{Running: false, Healthy: false}, err
	}
	ready := eng == nil && strings.TrimSpace(captured) != ""
	return mdriver.Health{Running: ready, Healthy: ready, Version: versionRe.FindString(captured)}, nil
}

// Version returns the IRIS release (e.g. "2026.1") for status/info/doctor.
func (s *Session) Version(ctx context.Context) (string, error) {
	h, err := s.Health(ctx)
	if err != nil {
		return "", err
	}
	return h.Version, nil
}

// SetGlobal sets @ref=value via an indirect set (data.set / fixture seeding).
func (s *Session) SetGlobal(ctx context.Context, ref, value string) error {
	cmd := "set @(" + irisString(ref) + ")=" + irisString(value)
	_, status, eng, err := s.runScript(ctx, s.trapLine(cmd))
	if err != nil {
		return err
	}
	if eng != nil {
		return fmt.Errorf("session: set %s failed: %s %s", ref, eng.Mnemonic, eng.Text)
	}
	if status != 0 {
		return fmt.Errorf("session: set %s returned status %d", ref, status)
	}
	return nil
}

// ReadGlobal reads $get(@ref) (data.get). The value is Base64-encoded in the
// session (like the remote runner's GetOut) so control bytes survive the noisy
// terminal capture intact.
func (s *Session) ReadGlobal(ctx context.Context, req mdriver.GlobalRef) (mdriver.GlobalNode, error) {
	cmd := "write $system.Encryption.Base64Encode($get(@(" + irisString(req.Ref) + ")))"
	captured, _, eng, err := s.runScript(ctx, s.trapLine(cmd))
	if err != nil {
		return mdriver.GlobalNode{}, err
	}
	if eng != nil {
		return mdriver.GlobalNode{}, fmt.Errorf("session: read %s failed: %s %s", req.Ref, eng.Mnemonic, eng.Text)
	}
	b64 := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, captured)
	if b64 == "" {
		return mdriver.GlobalNode{Ref: req.Ref}, nil
	}
	raw, derr := base64.StdEncoding.DecodeString(b64)
	if derr != nil {
		return mdriver.GlobalNode{}, fmt.Errorf("session: decode %s: %w", req.Ref, derr)
	}
	return mdriver.GlobalNode{Ref: req.Ref, Value: string(raw)}, nil
}

// Load stages routine source into the namespace and compiles it (exec.load). The
// neutral ".m" source maps to a ".int" docname with the UDL ROUTINE header (the
// same rules as the remote transport), is placed in the engine's filesystem
// (piped into the container for docker, written to a host temp file for local),
// then loaded with $SYSTEM.OBJ.Load(path,"ck"). A compile fault is returned as a
// LoadResult.EngineError, not a Go error.
func (s *Session) Load(ctx context.Context, req mdriver.LoadRequest) (mdriver.LoadResult, error) {
	files, err := expandPaths(req.Paths)
	if err != nil {
		return mdriver.LoadResult{}, err
	}
	var loaded []string
	for _, f := range files {
		content, rerr := os.ReadFile(f)
		if rerr != nil {
			return mdriver.LoadResult{}, rerr
		}
		name := req.Prefix + irisDocname(filepath.Base(f))
		body := irisRoutineLines(name, splitLines(string(content)))
		path, serr := s.stage(ctx, name, strings.Join(body, "\n")+"\n")
		if serr != nil {
			return mdriver.LoadResult{}, serr
		}
		inner := "set sc=$system.OBJ.Load(" + irisString(path) + `,"ck") ` +
			`if 'sc { set st=5,em="<COMPILE>||0|"_$system.Status.GetErrorText(sc) }`
		_, _, eng, lerr := s.runScript(ctx, s.trapLine(inner))
		if lerr != nil {
			return mdriver.LoadResult{}, lerr
		}
		if eng != nil {
			return mdriver.LoadResult{Loaded: loaded, EngineError: eng}, nil
		}
		loaded = append(loaded, name)
	}
	return mdriver.LoadResult{Loaded: loaded}, nil
}

// stage places source content where the engine can load it and returns the path
// the session should pass to $SYSTEM.OBJ.Load: piped into the container under
// docker, or a host temp file for local.
func (s *Session) stage(ctx context.Context, name, content string) (string, error) {
	path := "/tmp/" + name
	if s.isDocker() {
		argv := []string{"docker", "exec", "-i", s.cfg.Container, "sh", "-c", "cat > " + path}
		if _, err := s.run(ctx, argv, content); err != nil {
			return "", fmt.Errorf("session: stage %s into container: %w", name, err)
		}
		return path, nil
	}
	tmp, err := os.CreateTemp("", "miris-*-"+name)
	if err != nil {
		return "", err
	}
	defer tmp.Close()
	if _, err := tmp.WriteString(content); err != nil {
		return "", err
	}
	return tmp.Name(), nil
}

// Abort stops a run still in flight under the ephemeral prefix (exec.abort). The
// prefixed exec registered its process in ^mIrisRun(rid,"pid"); this terminates a
// live, not-"done" process (^$JOB liveness, never self) and writes the pid back.
// Driver-local, not a neutral Transport method (like m-ydb's Session.Abort).
func (s *Session) Abort(ctx context.Context, prefix string) ([]string, error) {
	rid := irisString(prefix)
	inner := "set pid=$get(^mIrisRun(" + rid + `,"pid"))` +
		` if pid'=""&'$data(^mIrisRun(` + rid + `,"done"))&(pid'=$job)&$data(^$JOB(pid)) { do $system.Process.Terminate(pid,2) set ^mIrisRun(` + rid + `,"aborted")=1 write pid }`
	captured, _, eng, err := s.runScript(ctx, s.trapLine(inner))
	if err != nil {
		return nil, err
	}
	if eng != nil {
		return nil, fmt.Errorf("session: abort %s failed: %s %s", prefix, eng.Mnemonic, eng.Text)
	}
	pid := strings.TrimSpace(captured)
	if pid == "" {
		return nil, nil
	}
	return []string{pid}, nil
}

// --- shared file helpers (mirror internal/remote) ----------------------------

// irisDocname maps a routine-source basename to a valid IRIS docname: neutral
// ".m" → ".int" (classic MUMPS), other IRIS extensions pass through.
func irisDocname(base string) string {
	if strings.EqualFold(filepath.Ext(base), ".m") {
		return strings.TrimSuffix(base, filepath.Ext(base)) + ".int"
	}
	return base
}

// irisRoutineLines prepends the UDL `ROUTINE <name> [Type=…]` header IRIS requires
// for a routine doc (else #16021), unless one is already present or the type is
// not a routine.
func irisRoutineLines(docname string, lines []string) []string {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(docname), "."))
	switch ext {
	case "int", "mac", "inc":
	default:
		return lines
	}
	if len(lines) > 0 && strings.HasPrefix(lines[0], "ROUTINE ") {
		return lines
	}
	name := strings.TrimSuffix(filepath.Base(docname), filepath.Ext(docname))
	return append([]string{fmt.Sprintf("ROUTINE %s [Type=%s]", name, strings.ToUpper(ext))}, lines...)
}

func splitLines(s string) []string { return strings.Split(strings.TrimRight(s, "\n"), "\n") }

func expandPaths(paths []string) ([]string, error) {
	var out []string
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			out = append(out, p)
			continue
		}
		entries, err := os.ReadDir(p)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if !e.IsDir() {
				out = append(out, filepath.Join(p, e.Name()))
			}
		}
	}
	return out, nil
}

// osRun is the production runner: it executes argv feeding stdin. A non-zero exit
// is a CmdOutput code, not a Go error — only a failure to launch is an error.
func osRun(ctx context.Context, argv []string, stdin string) (CmdOutput, error) {
	if len(argv) == 0 {
		return CmdOutput{}, errors.New("session: empty argv")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code, err = ee.ExitCode(), nil
		}
	}
	return CmdOutput{Stdout: out.String(), Stderr: errb.String(), Code: code}, err
}
