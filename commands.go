package main

import (
	"context"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vista-cloud-dev/m-iris/clikit"
	"github.com/vista-cloud-dev/m-iris/internal/atelier"
	"github.com/vista-cloud-dev/m-iris/internal/config"
	"github.com/vista-cloud-dev/m-iris/internal/manifest"
	"github.com/vista-cloud-dev/m-iris/internal/mirror"
)

// --- list --------------------------------------------------------------------

type listCmd struct{}

type listResult struct {
	Namespace string   `json:"namespace"`
	Count     int      `json:"count"`
	Routines  []string `json:"routines"`
}

func (listCmd) Run(cc *clikit.Context, conn *config.Conn) error {
	if err := conn.Validate(config.Need{Network: true}); err != nil {
		return usageErr(err)
	}
	acfg, err := conn.Atelier()
	if err != nil {
		return usageErr(err)
	}
	client, err := atelier.New(acfg)
	if err != nil {
		return runtimeErr(err)
	}
	docs, err := client.DocNames(context.Background(), conn.Type, "")
	if err != nil {
		return runtimeErr(err)
	}
	sel, err := selectDocs(docs, conn.Filter, conn.Package)
	if err != nil {
		return usageErr(err)
	}
	names := docNames(sel)

	if conn.Porcelain && !cc.JSON() {
		for _, n := range names {
			fmt.Fprintln(cc.Stdout, n)
		}
		return nil
	}
	return cc.Result(listResult{conn.Namespace, len(names), names}, func() {
		cc.Title(fmt.Sprintf("%s — %d routine(s)", conn.Namespace, len(names)))
		for _, n := range names {
			fmt.Fprintln(cc.Stdout, "  "+n)
		}
	})
}

// --- pull --------------------------------------------------------------------

type pullCmd struct {
	Full bool `help:"Ignore the manifest; re-pull every routine."`
}

type pullResult struct {
	Namespace string `json:"namespace"`
	Mirror    string `json:"mirror"`
	Fetched   int    `json:"fetched"`
	Unchanged int    `json:"unchanged"`
	Deleted   int    `json:"deleted"`
	Bytes     int    `json:"bytes"`
	DryRun    bool   `json:"dryRun,omitempty"`
}

func (c *pullCmd) Run(cc *clikit.Context, conn *config.Conn) error {
	if err := conn.Validate(config.Need{Network: true, Mirror: true}); err != nil {
		return usageErr(err)
	}
	layout := conn.Layout()

	man, err := manifest.Load(layout.ManifestPath())
	if err != nil {
		return runtimeErr(err)
	}
	if man == nil {
		man = manifest.New(conn.Instance, conn.Namespace)
	} else {
		man.Instance, man.Namespace = conn.Instance, conn.Namespace
	}

	acfg, err := conn.Atelier()
	if err != nil {
		return usageErr(err)
	}
	client, err := atelier.New(acfg)
	if err != nil {
		return runtimeErr(err)
	}
	ctx := context.Background()
	docs, err := client.DocNames(ctx, conn.Type, "")
	if err != nil {
		return runtimeErr(err)
	}
	sel, err := selectDocs(docs, conn.Filter, conn.Package)
	if err != nil {
		return usageErr(err)
	}
	server := tsMap(sel)
	diff := manifest.Compare(server, man)

	toFetch := diff.ToPull()
	healed := 0
	if c.Full {
		toFetch = docNames(sel)
	} else {
		// Self-heal: also re-fetch unchanged routines whose mirror file is
		// missing (deleted / partial write). Cheap stat per routine. Content
		// tampering (file present but hash differs) is caught by `verify` and
		// repaired by `pull --full` — re-hashing every file each pull is too
		// costly at namespace scale.
		inFetch := make(map[string]bool, len(toFetch))
		for _, n := range toFetch {
			inFetch[n] = true
		}
		for _, n := range diff.Unchanged {
			if _, statErr := os.Stat(layout.RoutinePath(n)); os.IsNotExist(statErr) && !inFetch[n] {
				toFetch = append(toFetch, n)
				inFetch[n] = true
				healed++
			}
		}
		sort.Strings(toFetch)
	}
	unchanged := len(diff.Unchanged) - healed
	toDelete := diff.Deleted

	if conn.DryRun {
		return cc.Result(pullResult{
			Namespace: conn.Namespace, Mirror: layout.NamespaceDir(),
			Fetched: len(toFetch), Unchanged: unchanged, Deleted: len(toDelete), DryRun: true,
		}, func() {
			cc.Title(conn.Namespace + " — pull plan (dry run)")
			cc.KV(
				[2]string{"to fetch", fmt.Sprint(len(toFetch))},
				[2]string{"unchanged", fmt.Sprint(unchanged)},
				[2]string{"to delete", fmt.Sprint(len(toDelete))},
				[2]string{"mirror", layout.NamespaceDir()},
			)
		})
	}

	fetched, totalBytes, err := fetchRoutines(ctx, client, layout, toFetch, conn.Concurrency, server, man)
	if err != nil {
		return runtimeErr(err)
	}
	for _, name := range toDelete {
		if rmErr := os.Remove(layout.RoutinePath(name)); rmErr != nil && !os.IsNotExist(rmErr) {
			return runtimeErr(rmErr)
		}
		delete(man.Routines, name)
	}
	man.PulledAt = time.Now().UTC().Format(time.RFC3339)
	if err := manifest.Save(layout.ManifestPath(), man); err != nil {
		return runtimeErr(err)
	}

	return cc.Result(pullResult{
		Namespace: conn.Namespace, Mirror: layout.NamespaceDir(),
		Fetched: fetched, Unchanged: unchanged, Deleted: len(toDelete), Bytes: totalBytes,
	}, func() {
		cc.Title(conn.Namespace + " — pull complete")
		cc.KV(
			[2]string{"fetched", fmt.Sprint(fetched)},
			[2]string{"unchanged", fmt.Sprint(unchanged)},
			[2]string{"deleted", fmt.Sprint(len(toDelete))},
			[2]string{"bytes", fmt.Sprint(totalBytes)},
			[2]string{"mirror", layout.NamespaceDir()},
		)
		fmt.Fprintln(cc.Stdout, cc.Success("mirror updated"))
	})
}

// fetchRoutines GETs each docname concurrently (bounded by concurrency), writes
// it to the mirror, and records the manifest entry. On the first error it
// cancels the rest and returns that error.
func fetchRoutines(ctx context.Context, client *atelier.Client, layout mirror.Layout,
	names []string, concurrency int, server map[string]string, man *manifest.Manifest) (fetched, totalBytes int, err error) {
	if len(names) == 0 {
		return 0, 0, nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	fail := func(e error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = e
			cancel()
		}
		mu.Unlock()
	}

	for _, name := range names {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(name string) {
			defer wg.Done()
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}
			doc, e := client.GetDoc(ctx, name)
			if e != nil {
				fail(e)
				return
			}
			wr, e := mirror.WriteRoutine(layout.RoutinePath(name), doc.Content)
			if e != nil {
				fail(e)
				return
			}
			mu.Lock()
			man.Routines[name] = manifest.Entry{ServerTS: server[name], SHA256: wr.SHA256, Bytes: wr.Bytes}
			totalBytes += wr.Bytes
			fetched++
			mu.Unlock()
		}(name)
	}
	wg.Wait()
	if firstErr != nil {
		return 0, 0, firstErr
	}
	return fetched, totalBytes, nil
}

// --- status ------------------------------------------------------------------

type statusCmd struct{}

type statusResult struct {
	Namespace string   `json:"namespace"`
	New       []string `json:"new"`
	Changed   []string `json:"changed"`
	Deleted   []string `json:"deleted"`
	Unchanged int      `json:"unchanged"`
	Drift     bool     `json:"drift"`
}

func (statusCmd) Run(cc *clikit.Context, conn *config.Conn) error {
	if err := conn.Validate(config.Need{Network: true, Mirror: true}); err != nil {
		return usageErr(err)
	}
	layout := conn.Layout()
	man, err := manifest.Load(layout.ManifestPath())
	if err != nil {
		return runtimeErr(err)
	}
	acfg, err := conn.Atelier()
	if err != nil {
		return usageErr(err)
	}
	client, err := atelier.New(acfg)
	if err != nil {
		return runtimeErr(err)
	}
	docs, err := client.DocNames(context.Background(), conn.Type, "")
	if err != nil {
		return runtimeErr(err)
	}
	sel, err := selectDocs(docs, conn.Filter, conn.Package)
	if err != nil {
		return usageErr(err)
	}
	d := manifest.Compare(tsMap(sel), man)
	res := statusResult{
		Namespace: conn.Namespace,
		New:       nonNil(d.New), Changed: nonNil(d.Changed), Deleted: nonNil(d.Deleted),
		Unchanged: len(d.Unchanged), Drift: d.Drift(),
	}
	return report(cc, res, func() {
		cc.Title(conn.Namespace + " — sync status")
		if conn.Porcelain {
			writePorcelainDiff(cc, d)
			return
		}
		renderDiff(cc, d)
	}, d.Drift(), "DRIFT",
		fmt.Sprintf("%d new, %d changed, %d deleted — mirror out of sync", len(d.New), len(d.Changed), len(d.Deleted)),
		"run 'm-iris sync pull' to update the mirror")
}

// --- verify ------------------------------------------------------------------

type verifyCmd struct{}

type verifyResult struct {
	Namespace string   `json:"namespace"`
	Checked   int      `json:"checked"`
	OK        int      `json:"ok"`
	Mismatch  []string `json:"mismatch"`
	Missing   []string `json:"missing"`
}

func (verifyCmd) Run(cc *clikit.Context, conn *config.Conn) error {
	if err := conn.Validate(config.Need{Mirror: true}); err != nil {
		return usageErr(err)
	}
	layout := conn.Layout()
	man, err := manifest.Load(layout.ManifestPath())
	if err != nil {
		return runtimeErr(err)
	}
	if man == nil {
		return clikit.Fail(clikit.ExitRuntime, "NO_MANIFEST",
			"no manifest at "+layout.ManifestPath()+"; run 'm-iris sync pull' first", "")
	}

	names, err := scopeManifest(man, conn.Filter, conn.Package)
	if err != nil {
		return usageErr(err)
	}
	var mismatch, missing []string
	okCount := 0
	for _, name := range names {
		e := man.Routines[name]
		sum, n, hErr := mirror.HashFile(layout.RoutinePath(name))
		switch {
		case os.IsNotExist(hErr):
			missing = append(missing, name)
		case hErr != nil:
			return runtimeErr(hErr)
		case sum != e.SHA256 || n != e.Bytes:
			mismatch = append(mismatch, name)
		default:
			okCount++
		}
	}
	drift := len(mismatch)+len(missing) > 0
	res := verifyResult{
		Namespace: conn.Namespace,
		Checked:   len(names), OK: okCount, Mismatch: nonNil(mismatch), Missing: nonNil(missing),
	}
	return report(cc, res, func() {
		cc.Title(conn.Namespace + " — verify mirror")
		for _, n := range missing {
			fmt.Fprintln(cc.Stdout, cc.Failure("missing  "+n))
		}
		for _, n := range mismatch {
			fmt.Fprintln(cc.Stdout, cc.Warning("mismatch "+n))
		}
		if !drift {
			fmt.Fprintln(cc.Stdout, cc.Success(fmt.Sprintf("verified %d routine(s) against the manifest", okCount)))
		}
	}, drift, "MISMATCH",
		fmt.Sprintf("%d mismatched, %d missing — mirror does not match the manifest", len(mismatch), len(missing)),
		"re-run 'm-iris sync pull' or investigate tampering")
}

// --- shared helpers ----------------------------------------------------------

// report emits a command result and signals drift/mismatch via exit 3. The full
// report is always written to stdout; CI gates on the exit code.
func report(cc *clikit.Context, data any, text func(), drift bool, code, summary, hint string) error {
	if err := cc.Result(data, text); err != nil {
		return err
	}
	if drift {
		return clikit.Fail(clikit.ExitCheck, code, summary, hint)
	}
	return nil
}

// nonNil returns s, or an empty (non-nil) slice so the JSON envelope renders an
// empty list as [] rather than null.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func runtimeErr(err error) error {
	return clikit.Fail(clikit.ExitRuntime, "RUNTIME", err.Error(), "")
}

func usageErr(err error) error {
	return clikit.Fail(clikit.ExitUsage, "BAD_CONFIG", err.Error(), "set flags or M_IRIS_* env vars")
}

// selectDocs filters a docnames listing by package prefix and glob filter.
func selectDocs(docs []atelier.DocName, glob, pkg string) ([]atelier.DocName, error) {
	out := make([]atelier.DocName, 0, len(docs))
	for _, d := range docs {
		if d.Name == "" {
			continue
		}
		ok, err := match(d.Name, glob, pkg)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, d)
		}
	}
	return out, nil
}

// scopeManifest returns the manifest's docnames (sorted) that pass the filter.
func scopeManifest(man *manifest.Manifest, glob, pkg string) ([]string, error) {
	all := sortedKeys(man.Routines)
	out := make([]string, 0, len(all))
	for _, n := range all {
		ok, err := match(n, glob, pkg)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, n)
		}
	}
	return out, nil
}

// match reports whether docname passes the package prefix and glob filter.
// An empty pkg/glob matches everything.
func match(docname, glob, pkg string) (bool, error) {
	if pkg != "" && !strings.HasPrefix(docname, pkg) {
		return false, nil
	}
	if glob != "" {
		ok, err := path.Match(glob, docname)
		if err != nil {
			return false, fmt.Errorf("invalid --filter %q: %w", glob, err)
		}
		return ok, nil
	}
	return true, nil
}

func docNames(docs []atelier.DocName) []string {
	names := make([]string, 0, len(docs))
	for _, d := range docs {
		names = append(names, d.Name)
	}
	sort.Strings(names)
	return names
}

func tsMap(docs []atelier.DocName) map[string]string {
	m := make(map[string]string, len(docs))
	for _, d := range docs {
		m[d.Name] = d.TS
	}
	return m
}

func sortedKeys(m map[string]manifest.Entry) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func renderDiff(cc *clikit.Context, d manifest.Diff) {
	section := func(label string, names []string) {
		if len(names) == 0 {
			return
		}
		cc.Rule(label)
		for _, n := range names {
			fmt.Fprintln(cc.Stdout, "  "+n)
		}
	}
	section("new", d.New)
	section("changed", d.Changed)
	section("deleted", d.Deleted)
	if !d.Drift() {
		fmt.Fprintln(cc.Stdout, cc.Success(fmt.Sprintf("in sync — %d routine(s)", len(d.Unchanged))))
		return
	}
	fmt.Fprintf(cc.Stdout, "%s   %s new   %s changed   %s deleted   %d unchanged\n",
		cc.Warning("drift"),
		cc.Badge("info", fmt.Sprint(len(d.New))),
		cc.Badge("info", fmt.Sprint(len(d.Changed))),
		cc.Badge("err", fmt.Sprint(len(d.Deleted))),
		len(d.Unchanged))
}

func writePorcelainDiff(cc *clikit.Context, d manifest.Diff) {
	for _, n := range d.New {
		fmt.Fprintln(cc.Stdout, "new\t"+n)
	}
	for _, n := range d.Changed {
		fmt.Fprintln(cc.Stdout, "changed\t"+n)
	}
	for _, n := range d.Deleted {
		fmt.Fprintln(cc.Stdout, "deleted\t"+n)
	}
}
