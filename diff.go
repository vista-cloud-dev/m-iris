package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vista-cloud-dev/m-iris/clikit"
	"github.com/vista-cloud-dev/m-iris/internal/atelier"
	"github.com/vista-cloud-dev/m-iris/internal/config"
	"github.com/vista-cloud-dev/m-iris/internal/udiff"
)

// syncDiffCmd shows a unified diff of one routine: the instance copy (over
// Atelier) versus the local mirror — or a --from directory. It is read-only on
// both sides (driver-contract §5.2: `{ unified }`). A side absent on the
// instance or on disk is treated as empty, so the diff renders a pure addition
// or deletion rather than erroring.
type syncDiffCmd struct {
	Name string `arg:"" help:"Routine to diff (bare name or NAME.mac)."`
	From string `help:"Compare the instance against this directory instead of the mirror." placeholder:"DIR"`
}

type syncDiffResult struct {
	Unified string `json:"unified"`
}

func (c *syncDiffCmd) Run(cc *clikit.Context, conn *config.Conn) error {
	if err := conn.Validate(config.Need{Network: true}); err != nil {
		return usageErr(err)
	}
	name := routineFile(c.Name, conn.Type)
	ctx := context.Background()

	acfg, err := conn.Atelier()
	if err != nil {
		return usageErr(err)
	}
	client, err := atelier.New(acfg)
	if err != nil {
		return runtimeErr(err)
	}

	// Instance side: fetch only if the routine exists (Stat distinguishes
	// not-found cleanly), so an absent routine diffs as empty rather than error.
	var instLines []string
	if _, exists, sErr := client.Stat(ctx, name); sErr != nil {
		return runtimeErr(sErr)
	} else if exists {
		doc, dErr := client.GetDoc(ctx, name)
		if dErr != nil {
			return runtimeErr(dErr)
		}
		instLines = normalizeLines(doc.Content)
	}

	// Local side: the mirror file, or a file under --from.
	localPath := conn.Layout().RoutinePath(name)
	bLabel := "mirror/" + name
	if c.From != "" {
		localPath = filepath.Join(c.From, name)
		bLabel = filepath.Join(c.From, name)
	}
	localBytes, lErr := os.ReadFile(localPath)
	if lErr != nil && !os.IsNotExist(lErr) {
		return runtimeErr(lErr)
	}

	u := udiff.Unified("instance/"+name, bLabel, instLines, udiff.SplitLines(string(localBytes)))

	return cc.Result(syncDiffResult{Unified: u}, func() {
		if u == "" {
			fmt.Fprintln(cc.Stdout, cc.Success(name+": no differences"))
			return
		}
		fmt.Fprint(cc.Stdout, u)
	})
}

// normalizeLines strips any stray CR a server line carries, so line-ending
// differences don't show up as diffs (parity with mirror.WriteRoutine).
func normalizeLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = strings.TrimRight(l, "\r\n")
	}
	return out
}

// routineFile normalizes a routine argument to its docname: a bare "DGREG"
// becomes "DGREG.<type>", while an argument that already carries a routine
// extension (.mac/.int/.inc) is used verbatim.
func routineFile(name, typ string) string {
	for _, ext := range []string{".mac", ".int", ".inc"} {
		if strings.HasSuffix(name, ext) {
			return name
		}
	}
	return name + "." + typ
}
