package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/vista-cloud-dev/irissync/clikit"
	"github.com/vista-cloud-dev/irissync/internal/atelier"
	"github.com/vista-cloud-dev/irissync/internal/config"
	"github.com/vista-cloud-dev/irissync/internal/lock"
	"github.com/vista-cloud-dev/irissync/internal/manifest"
	"github.com/vista-cloud-dev/irissync/internal/mirror"
)

// pushCmd writes edited routines from the mirror back to IRIS — the sole DB
// writer. It is the opt-in write path: the read verbs (list/pull/status/verify)
// never write, so the only way to change IRIS source through irissync is this
// command, gated by three single-writer layers (liberation-binary-design §5):
//
//  1. a local exclusive lock (internal/lock) serializes concurrent pushes;
//  2. a manifest conflict-check refuses (exit 4) any routine the server changed
//     since pull — the cross-writer guard — unless --force;
//  3. detect-and-defer: a routine the server marks non-updatable (e.g. held by
//     %Studio.SourceControl / the ObjectScript extension) is deferred, not
//     fought over, unless --force.
//
// Each pushed routine is PUT then compiled-on-import to validate the write and
// generate the read-only .int; the manifest entry is refreshed to the new
// server timestamp so the next status/verify is accurate.
type pushCmd struct {
	Force     bool          `help:"Push even if the server copy changed since pull, or is held by another writer (override the conflict-check and detect-and-defer)."`
	LockTTL   time.Duration `name:"lock-ttl" default:"15m" help:"Reclaim a stale push lock older than this."`
	NoCompile bool          `name:"no-compile" help:"Skip the post-import compile (compile is on by default)."`
}

type pushItem struct {
	Name     string `json:"name"`
	Status   string `json:"status"` // pushed | conflict | deferred | compile-error | up-to-date
	Detail   string `json:"detail,omitempty"`
	ServerTS string `json:"serverTS,omitempty"`
}

type pushResult struct {
	Namespace     string     `json:"namespace"`
	Mirror        string     `json:"mirror"`
	Pushed        int        `json:"pushed"`
	Conflicts     int        `json:"conflicts"`
	Deferred      int        `json:"deferred"`
	CompileErrors int        `json:"compileErrors"`
	UpToDate      int        `json:"upToDate"`
	Compiled      bool       `json:"compiled"`
	Items         []pushItem `json:"items"`
	DryRun        bool       `json:"dryRun,omitempty"`
	Reclaimed     bool       `json:"reclaimedLock,omitempty"`
}

func (c *pushCmd) Run(cc *clikit.Context, conn *config.Conn) error {
	if err := conn.Validate(config.Need{Network: true, Mirror: true}); err != nil {
		return usageErr(err)
	}
	layout := conn.Layout()

	man, err := manifest.Load(layout.ManifestPath())
	if err != nil {
		return runtimeErr(err)
	}
	if man == nil {
		return clikit.Fail(clikit.ExitRuntime, "NO_MANIFEST",
			"no manifest at "+layout.ManifestPath()+"; run 'irissync pull' first", "push requires a pulled mirror as its conflict-check basis")
	}

	// Candidate routines: the manifest's docnames, filtered, that have a mirror
	// file on disk. push writes what is in the mirror — it does not invent
	// routines from thin air.
	names, err := scopeManifest(man, conn.Filter, conn.Package)
	if err != nil {
		return usageErr(err)
	}
	present := make([]string, 0, len(names))
	for _, n := range names {
		if _, statErr := os.Stat(layout.RoutinePath(n)); statErr == nil {
			present = append(present, n)
		}
	}
	sort.Strings(present)

	acfg, err := conn.Atelier()
	if err != nil {
		return usageErr(err)
	}
	client, err := atelier.New(acfg)
	if err != nil {
		return runtimeErr(err)
	}
	ctx := context.Background()

	// Detect-and-defer (§5 layer 3) keys on the docnames `upd` flag: a routine
	// the server marks non-updatable is held by another writer (e.g. the
	// ObjectScript extension / %Studio.SourceControl), so we defer rather than
	// fight for it. One docnames listing builds the upd map for all candidates.
	docs, err := client.DocNames(ctx, conn.Type, "")
	if err != nil {
		return runtimeErr(err)
	}
	updatable := updMap(docs)

	// Plan: conflict-check + detect-and-defer for every candidate, reading the
	// live server state. This runs for both dry-run and the real push.
	plan, err := c.planPush(ctx, client, layout, man, present, updatable)
	if err != nil {
		return runtimeErr(err)
	}

	if conn.DryRun {
		return c.emit(cc, conn, layout, plan, true, false)
	}

	// Acquire the single-writer lock only when we are about to write.
	if hasWritable(plan) {
		lk, lockErr := lock.Acquire(layout.PushLockPath(), c.LockTTL)
		if lockErr != nil {
			var held *lock.HeldError
			if asHeldLock(lockErr, &held) {
				return clikit.Fail(clikit.ExitRefused, "LOCK_HELD", held.Error(),
					"another push holds the lock; wait for it to finish or pass --lock-ttl to reclaim a stale lock")
			}
			return runtimeErr(lockErr)
		}
		defer func() { _ = lk.Release() }()
		plan.reclaimed = lk.Reclaimed()

		if err := c.writePlan(ctx, client, layout, man, plan); err != nil {
			return runtimeErr(err)
		}
		man.PulledAt = time.Now().UTC().Format(time.RFC3339)
		if err := manifest.Save(layout.ManifestPath(), man); err != nil {
			return runtimeErr(err)
		}
	}

	return c.emit(cc, conn, layout, plan, false, plan.reclaimed)
}

// pushPlan is the classified set of candidate routines.
type pushPlan struct {
	writable     []string // safe to PUT
	conflicts    []pushItem
	deferred     []pushItem
	compileError []pushItem // PUT succeeded but compile reported diagnostics
	upToDate     []string
	reclaimed    bool
	serverTS     map[string]string // live server ts at plan time, for the manifest update
}

func hasWritable(p *pushPlan) bool { return len(p.writable) > 0 }

// planPush classifies each candidate routine against the live server state:
// detect-and-defer (server marks the doc non-updatable) and conflict-check
// (manifest vs. server timestamp). With --force, conflicts and defers become
// writable. updatable maps a docname to the server's `upd` flag; a docname
// absent from the map (not currently on the server) is treated as updatable
// (it would be a fresh create).
func (c *pushCmd) planPush(ctx context.Context, client *atelier.Client, layout mirror.Layout,
	man *manifest.Manifest, names []string, updatable map[string]bool) (*pushPlan, error) {
	p := &pushPlan{serverTS: map[string]string{}}
	for _, name := range names {
		stat, exists, err := client.Stat(ctx, name)
		if err != nil {
			return nil, err
		}
		p.serverTS[name] = stat.TS

		// Detect-and-defer: a doc the server lists as non-updatable is held by
		// another writer (e.g. source control). Defer unless forced.
		if upd, listed := updatable[name]; exists && listed && !upd && !c.Force {
			p.deferred = append(p.deferred, pushItem{
				Name: name, Status: "deferred", ServerTS: stat.TS,
				Detail: "server marks the document non-updatable (held by another writer / source control)",
			})
			continue
		}

		conf := manifest.CheckConflict(man, name, stat.TS, exists)
		if conf.Kind != manifest.ConflictNone && !c.Force {
			p.conflicts = append(p.conflicts, pushItem{
				Name: name, Status: "conflict", Detail: conf.Message, ServerTS: stat.TS,
			})
			continue
		}

		// Up-to-date short-circuit: the local file already matches the server.
		// Compare the recorded manifest hash against the live server state via
		// the timestamp — if nothing changed locally and the server matches, no
		// PUT is needed. We detect "nothing to push" by comparing the on-disk
		// hash to the manifest entry (an unchanged file the server still matches).
		if conf.Kind == manifest.ConflictNone && exists && localMatchesManifest(layout, man, name) && stat.TS == man.Routines[name].ServerTS {
			p.upToDate = append(p.upToDate, name)
			continue
		}
		p.writable = append(p.writable, name)
	}
	sort.Strings(p.writable)
	sort.Strings(p.upToDate)
	return p, nil
}

// writePlan PUTs each writable routine, compiles them (unless --no-compile),
// and refreshes the manifest entries to the new server timestamps + hashes.
func (c *pushCmd) writePlan(ctx context.Context, client *atelier.Client, layout mirror.Layout,
	man *manifest.Manifest, p *pushPlan) error {
	for _, name := range p.writable {
		lines, sum, n, err := readRoutine(layout.RoutinePath(name))
		if err != nil {
			return fmt.Errorf("read mirror file %s: %w", name, err)
		}
		res, err := client.PutDoc(ctx, name, lines)
		if err != nil {
			return fmt.Errorf("PUT %s: %w", name, err)
		}
		man.Routines[name] = manifest.Entry{ServerTS: res.TS, SHA256: sum, Bytes: n}
		p.serverTS[name] = res.TS
	}

	if c.NoCompile || len(p.writable) == 0 {
		return nil
	}
	comp, err := client.Compile(ctx, p.writable, "cuk")
	if err != nil {
		return fmt.Errorf("compile: %w", err)
	}
	if !comp.OK() {
		// A compile failure leaves the source saved (and the manifest updated to
		// the new server state) but flagged. The write itself succeeded, so this
		// is NOT a refusal (exit 4) — it is a finding (exit 3), surfaced as
		// compile-error items distinct from the conflict/deferred refusals (§5).
		for _, d := range comp.Diagnostics {
			p.compileError = append(p.compileError, pushItem{Status: "compile-error", Detail: d})
		}
	}
	return nil
}

// emit renders the push result and chooses the exit code. Exit 4 (refused) when
// any routine was refused pre-write (conflict or deferred — nothing was
// clobbered). Exit 3 (findings) when writes succeeded but the compile reported
// diagnostics — the source is saved but flagged. Otherwise exit 0.
func (c *pushCmd) emit(cc *clikit.Context, conn *config.Conn, layout mirror.Layout, p *pushPlan, dryRun, reclaimed bool) error {
	items := make([]pushItem, 0, len(p.writable)+len(p.upToDate)+len(p.conflicts)+len(p.deferred)+len(p.compileError))
	for _, n := range p.writable {
		status := "pushed"
		if dryRun {
			status = "to-push"
		}
		items = append(items, pushItem{Name: n, Status: status, ServerTS: p.serverTS[n]})
	}
	for _, n := range p.upToDate {
		items = append(items, pushItem{Name: n, Status: "up-to-date"})
	}
	items = append(items, p.conflicts...)
	items = append(items, p.deferred...)
	items = append(items, p.compileError...)

	pushed := len(p.writable)
	if dryRun {
		pushed = 0
	}
	res := pushResult{
		Namespace: conn.Namespace, Mirror: layout.NamespaceDir(),
		Pushed: pushed, Conflicts: len(p.conflicts), Deferred: len(p.deferred),
		CompileErrors: len(p.compileError),
		UpToDate:      len(p.upToDate), Compiled: !c.NoCompile && hasWritable(p) && !dryRun,
		Items: items, DryRun: dryRun, Reclaimed: reclaimed,
	}

	refused := len(p.conflicts)+len(p.deferred) > 0
	compileFailed := len(p.compileError) > 0
	textFn := func() {
		title := conn.Namespace + " — push"
		if dryRun {
			title += " plan (dry run)"
		}
		cc.Title(title)
		if reclaimed {
			fmt.Fprintln(cc.Stderr, cc.Warning("reclaimed a stale push lock"))
		}
		for _, it := range p.conflicts {
			fmt.Fprintln(cc.Stdout, cc.Failure("conflict "+it.Name+" — "+it.Detail))
		}
		for _, it := range p.deferred {
			fmt.Fprintln(cc.Stdout, cc.Warning("deferred "+it.Name+" "+it.Detail))
		}
		for _, it := range p.compileError {
			fmt.Fprintln(cc.Stdout, cc.Warning("compile  "+it.Detail))
		}
		verb := "pushed"
		if dryRun {
			verb = "to push"
		}
		cc.KV(
			[2]string{verb, fmt.Sprint(len(p.writable))},
			[2]string{"up-to-date", fmt.Sprint(len(p.upToDate))},
			[2]string{"conflicts", fmt.Sprint(len(p.conflicts))},
			[2]string{"deferred", fmt.Sprint(len(p.deferred))},
			[2]string{"compile errors", fmt.Sprint(len(p.compileError))},
			[2]string{"mirror", layout.NamespaceDir()},
		)
		if !refused && !compileFailed && !dryRun {
			fmt.Fprintln(cc.Stdout, cc.Success(fmt.Sprintf("pushed %d routine(s)", len(p.writable))))
		}
	}

	if err := cc.Result(res, textFn); err != nil {
		return err
	}
	if refused {
		return clikit.Fail(clikit.ExitRefused, "PUSH_REFUSED",
			fmt.Sprintf("%d conflict(s), %d deferred — refused to clobber concurrent server changes", len(p.conflicts), len(p.deferred)),
			"re-pull to reconcile, or pass --force to override the conflict-check")
	}
	if compileFailed {
		return clikit.Fail(clikit.ExitCheck, "COMPILE_ERROR",
			fmt.Sprintf("%d compile diagnostic(s) — source was written but did not compile cleanly", len(p.compileError)),
			"fix the routine source and push again")
	}
	return nil
}

// localMatchesManifest reports whether the on-disk routine still hashes to the
// manifest entry (i.e. it was not edited since pull).
func localMatchesManifest(layout mirror.Layout, man *manifest.Manifest, name string) bool {
	e, ok := man.Routines[name]
	if !ok {
		return false
	}
	sum, n, err := mirror.HashFile(layout.RoutinePath(name))
	if err != nil {
		return false
	}
	return sum == e.SHA256 && n == e.Bytes
}

// updMap builds docname → updatable from a docnames listing. The Atelier `upd`
// flag is true when the server will accept a write to that document.
func updMap(docs []atelier.DocName) map[string]bool {
	m := make(map[string]bool, len(docs))
	for _, d := range docs {
		m[d.Name] = d.Upd
	}
	return m
}

// readRoutine reads a mirror routine file as a line array (for PUT) and its
// content hash + byte length (for the refreshed manifest entry). Trailing
// newlines are not emitted as a spurious empty final line.
func readRoutine(path string) (lines []string, sum string, n int, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", 0, err
	}
	h := sha256.Sum256(data)
	body := strings.TrimRight(string(data), "\n")
	if body == "" {
		return []string{}, hex.EncodeToString(h[:]), len(data), nil
	}
	return strings.Split(body, "\n"), hex.EncodeToString(h[:]), len(data), nil
}

func asHeldLock(err error, target **lock.HeldError) bool {
	return errors.As(err, target)
}
