package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/vista-cloud-dev/m-iris/clikit"
	"github.com/vista-cloud-dev/m-iris/internal/atelier"
	"github.com/vista-cloud-dev/m-iris/internal/config"
	"github.com/vista-cloud-dev/m-iris/internal/manifest"
)

// syncRmCmd removes one routine from the instance (DELETE over Atelier), the
// local mirror, and the manifest — the delete counterpart to push
// (driver-contract §5.2: `{ removed }`). A routine already absent on the
// instance is reported but not an error (the desired end state). --dry-run
// reports the plan without touching anything.
type syncRmCmd struct {
	Name string `arg:"" help:"Routine to remove (bare name or NAME.mac)."`
}

type syncRmResult struct {
	Removed []string `json:"removed"`
	DryRun  bool     `json:"dryRun,omitempty"`
}

func (c *syncRmCmd) Run(cc *clikit.Context, conn *config.Conn) error {
	if err := conn.Validate(config.Need{Network: true, Mirror: true}); err != nil {
		return usageErr(err)
	}
	name := routineFile(c.Name, conn.Type)
	ctx := context.Background()
	layout := conn.Layout()

	acfg, err := conn.Atelier()
	if err != nil {
		return usageErr(err)
	}
	client, err := atelier.New(acfg)
	if err != nil {
		return runtimeErr(err)
	}

	// A routine counts as removable if it exists on the instance or in the mirror.
	_, onInstance, sErr := client.Stat(ctx, name)
	if sErr != nil {
		return runtimeErr(sErr)
	}
	_, statErr := os.Stat(layout.RoutinePath(name))
	inMirror := statErr == nil
	exists := onInstance || inMirror

	var removed []string
	if exists {
		removed = []string{name}
	}

	if conn.DryRun {
		return cc.Result(syncRmResult{Removed: nonNil(removed), DryRun: true}, func() {
			cc.Title("rm plan (dry run)")
			fmt.Fprintln(cc.Stdout, "  would remove "+strings.Join(nonNil(removed), ", "))
		})
	}

	if exists {
		if onInstance {
			if err := client.DeleteDoc(ctx, name); err != nil {
				return runtimeErr(err)
			}
		}
		if err := os.Remove(layout.RoutinePath(name)); err != nil && !os.IsNotExist(err) {
			return runtimeErr(err)
		}
		man, mErr := manifest.Load(layout.ManifestPath())
		if mErr != nil {
			return runtimeErr(mErr)
		}
		if man != nil {
			if _, ok := man.Routines[name]; ok {
				delete(man.Routines, name)
				if err := manifest.Save(layout.ManifestPath(), man); err != nil {
					return runtimeErr(err)
				}
			}
		}
	}

	return cc.Result(syncRmResult{Removed: nonNil(removed)}, func() {
		if len(removed) == 0 {
			fmt.Fprintln(cc.Stdout, cc.Warning(name+": not present on the instance or in the mirror"))
			return
		}
		fmt.Fprintln(cc.Stdout, cc.Success("removed "+name))
	})
}
