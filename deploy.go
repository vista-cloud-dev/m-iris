package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vista-cloud-dev/m-iris/clikit"
	"github.com/vista-cloud-dev/m-iris/internal/atelier"
	"github.com/vista-cloud-dev/m-iris/internal/config"
)

// deployCmd installs a library of routine source (e.g. m-stdlib/src) into an IRIS
// namespace over the official Atelier REST API — PUT each routine, then compile.
// Unlike push (which writes back an edited *mirror* under a conflict-check), deploy
// is a one-way install of a source tree: it has no manifest basis, so it always
// overwrites the namespace copy with the source on disk. That is the intended
// semantic for shipping a versioned library — re-running deploy upgrades in place.
//
// --prune makes it a true sync: routines on the server that share the deployed
// set's common name prefix but are absent from the source are deleted (e.g. a
// module dropped between releases). A safety guard refuses to prune unless the
// deployed set has a coherent common prefix of at least minPrunePrefix chars, so
// the prune scope can never widen to unrelated routines (e.g. VistA's).
type deployCmd struct {
	Paths     []string `arg:"" optional:"" type:"path" help:"Source dirs or .m files to install (default: src)."`
	NoCompile bool     `name:"no-compile" help:"Skip the post-import compile (compile is on by default)."`
	Prune     bool     `help:"Delete server routines in the deployed set's name-prefix that are absent from the source (true sync)."`
}

// minPrunePrefix is the shortest common routine-name prefix --prune will act on.
// "STD" (m-stdlib) is exactly 3; anything shorter is too broad to delete safely.
const minPrunePrefix = 3

type deployItem struct {
	Routine string `json:"routine"`
	Status  string `json:"status"` // installed | compile-error | pruned
	Detail  string `json:"detail,omitempty"`
}

type deployResult struct {
	Namespace    string       `json:"namespace"`
	Installed    int          `json:"installed"`
	Pruned       int          `json:"pruned"`
	CompileError int          `json:"compileErrors"`
	Compiled     bool         `json:"compiled"`
	PrunePrefix  string       `json:"prunePrefix,omitempty"`
	DryRun       bool         `json:"dryRun,omitempty"`
	Items        []deployItem `json:"items"`
}

func (c *deployCmd) Run(cc *clikit.Context, conn *config.Conn) error {
	if err := conn.Validate(config.Need{Network: true}); err != nil {
		return usageErr(err)
	}

	paths := c.Paths
	if len(paths) == 0 {
		paths = []string{"src"}
	}
	files, err := collectMFiles(paths)
	if err != nil {
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
	ctx := context.Background()

	// Map docname → file, and keep a sorted docname list for deterministic order.
	docToFile := map[string]string{}
	var docnames []string
	for _, f := range files {
		d := deployDocname(f)
		docToFile[d] = f
		docnames = append(docnames, d)
	}
	sort.Strings(docnames)

	res := deployResult{Namespace: conn.Namespace, DryRun: conn.DryRun}

	// Plan prune up front (a read) so a bad --prune scope fails before any write.
	var orphans []string
	if c.Prune {
		serverDocs, err := client.DocNames(ctx, conn.Type, "")
		if err != nil {
			return runtimeErr(err)
		}
		names := make([]string, 0, len(serverDocs))
		for _, d := range serverDocs {
			names = append(names, d.Name)
		}
		var prefix string
		orphans, prefix, err = prunePlan(docnames, names)
		if err != nil {
			return clikit.Fail(clikit.ExitUsage, "PRUNE_SCOPE",
				err.Error(), "deploy a prefix-coherent set (e.g. all STD*), or drop --prune")
		}
		res.PrunePrefix = prefix
	}

	if conn.DryRun {
		for _, d := range docnames {
			res.Items = append(res.Items, deployItem{Routine: d, Status: "to-install"})
		}
		for _, d := range orphans {
			res.Items = append(res.Items, deployItem{Routine: d, Status: "to-prune"})
		}
		res.Installed, res.Pruned = len(docnames), len(orphans)
		return c.emit(cc, res, false)
	}

	// Install: PUT each routine's source, then compile the whole set once.
	for _, d := range docnames {
		lines, err := readLines(docToFile[d])
		if err != nil {
			return runtimeErr(fmt.Errorf("read %s: %w", docToFile[d], err))
		}
		if _, err := client.PutDoc(ctx, d, lines); err != nil {
			return runtimeErr(fmt.Errorf("PUT %s: %w", d, err))
		}
		res.Items = append(res.Items, deployItem{Routine: d, Status: "installed"})
		res.Installed++
	}

	compileFailed := false
	if !c.NoCompile && len(docnames) > 0 {
		res.Compiled = true
		comp, err := client.Compile(ctx, docnames, "cuk")
		if err != nil {
			return runtimeErr(fmt.Errorf("compile: %w", err))
		}
		if !comp.OK() {
			compileFailed = true
			for _, d := range comp.Diagnostics {
				res.Items = append(res.Items, deployItem{Status: "compile-error", Detail: d})
				res.CompileError++
			}
		}
	}

	// Prune after a clean install: delete orphaned routines no longer shipped.
	for _, d := range orphans {
		if err := client.DeleteDoc(ctx, d); err != nil {
			return runtimeErr(fmt.Errorf("prune %s: %w", d, err))
		}
		res.Items = append(res.Items, deployItem{Routine: d, Status: "pruned"})
		res.Pruned++
	}

	return c.emit(cc, res, compileFailed)
}

func (c *deployCmd) emit(cc *clikit.Context, res deployResult, compileFailed bool) error {
	textFn := func() {
		title := res.Namespace + " — deploy"
		if res.DryRun {
			title += " plan (dry run)"
		}
		cc.Title(title)
		cc.KV(
			[2]string{"installed", fmt.Sprint(res.Installed)},
			[2]string{"pruned", fmt.Sprint(res.Pruned)},
			[2]string{"compile errors", fmt.Sprint(res.CompileError)},
			[2]string{"namespace", cc.Accent(res.Namespace)},
		)
		for _, it := range res.Items {
			if it.Status == "compile-error" {
				fmt.Fprintln(cc.Stdout, cc.Warning("compile  "+it.Detail))
			}
		}
		if !compileFailed && !res.DryRun {
			fmt.Fprintln(cc.Stdout, cc.Success(fmt.Sprintf("installed %d routine(s)%s", res.Installed, prunedSuffix(res.Pruned))))
		}
	}
	if err := cc.Result(res, textFn); err != nil {
		return err
	}
	if compileFailed {
		return clikit.Fail(clikit.ExitCheck, "COMPILE_ERROR",
			fmt.Sprintf("%d compile diagnostic(s) — source was installed but did not compile cleanly", res.CompileError),
			"fix the routine source and deploy again")
	}
	return nil
}

func prunedSuffix(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(", pruned %d", n)
}

// deployDocname maps a source file path to its IRIS routine docname: the base
// name, sans extension, upper-cased, with a ".mac" suffix (routines are MAC
// source). e.g. "../m-stdlib/src/STDJSON.m" → "STDJSON.mac".
func deployDocname(path string) string {
	base := filepath.Base(path)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	return strings.ToUpper(stem) + ".mac"
}

// commonStemPrefix returns the longest common prefix of the upper-cased routine
// stems (a "stem" is a docname without its .mac extension). "" when the stems
// share nothing — which --prune treats as too broad to act on.
func commonStemPrefix(stems []string) string {
	if len(stems) == 0 {
		return ""
	}
	prefix := strings.ToUpper(stems[0])
	for _, s := range stems[1:] {
		s = strings.ToUpper(s)
		n := 0
		for n < len(prefix) && n < len(s) && prefix[n] == s[n] {
			n++
		}
		prefix = prefix[:n]
		if prefix == "" {
			break
		}
	}
	return prefix
}

// prunePlan computes which server routines to delete for a true sync: those in
// the deployed set's common name prefix but absent from the deployed set. It
// refuses (error) unless that common prefix is at least minPrunePrefix chars, so
// the delete scope can never widen to unrelated routines. Returned orphans are
// the server's verbatim docnames (so the DELETE targets exactly what it listed).
func prunePlan(deployedDocnames, serverDocnames []string) (orphans []string, prefix string, err error) {
	deployedStems := make([]string, 0, len(deployedDocnames))
	deployedSet := map[string]bool{}
	for _, d := range deployedDocnames {
		stem := docStem(d)
		deployedStems = append(deployedStems, stem)
		deployedSet[stem] = true
	}
	prefix = commonStemPrefix(deployedStems)
	if len(prefix) < minPrunePrefix {
		return nil, prefix, fmt.Errorf("prune refused: deployed routines share no common name prefix of at least %d chars (got %q)", minPrunePrefix, prefix)
	}
	for _, sd := range serverDocnames {
		stem := docStem(sd)
		if strings.HasPrefix(stem, prefix) && !deployedSet[stem] {
			orphans = append(orphans, sd)
		}
	}
	sort.Strings(orphans)
	return orphans, prefix, nil
}

// docStem upper-cases a docname and strips its extension for comparison.
func docStem(docname string) string {
	return strings.ToUpper(strings.TrimSuffix(docname, filepath.Ext(docname)))
}

// collectMFiles expands paths (dirs → their *.m, .m files passed through) into a
// sorted, de-duplicated list of routine source files.
func collectMFiles(paths []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			ms, err := filepath.Glob(filepath.Join(p, "*.m"))
			if err != nil {
				return nil, err
			}
			for _, m := range ms {
				if !seen[m] {
					seen[m] = true
					out = append(out, m)
				}
			}
		} else if strings.HasSuffix(p, ".m") && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no .m routines found in %s", strings.Join(paths, ", "))
	}
	sort.Strings(out)
	return out, nil
}

// readLines reads a routine source file as the line array Atelier PUT expects
// (a trailing newline is not emitted as a spurious empty final line).
func readLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	body := strings.TrimRight(string(data), "\n")
	if body == "" {
		return []string{}, nil
	}
	return strings.Split(body, "\n"), nil
}
