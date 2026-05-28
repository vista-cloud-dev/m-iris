package manifest

import "sort"

// Diff is the classification of a server docname listing against the manifest.
// Each field holds docnames, sorted.
type Diff struct {
	New       []string `json:"new"`
	Changed   []string `json:"changed"`
	Deleted   []string `json:"deleted"`
	Unchanged []string `json:"unchanged"`
}

// Drift reports whether the mirror is out of sync with the server (anything
// new, changed, or deleted). Unchanged-only is in sync.
func (d Diff) Drift() bool {
	return len(d.New)+len(d.Changed)+len(d.Deleted) > 0
}

// ToPull returns the docnames pull must fetch: new + changed, sorted.
func (d Diff) ToPull() []string {
	out := make([]string, 0, len(d.New)+len(d.Changed))
	out = append(out, d.New...)
	out = append(out, d.Changed...)
	sort.Strings(out)
	return out
}

// Compare classifies server docnames (docname → server timestamp) against the
// manifest. A nil manifest is treated as empty (nothing pulled yet), so every
// server routine is New.
func Compare(server map[string]string, m *Manifest) Diff {
	recorded := map[string]Entry{}
	if m != nil {
		recorded = m.Routines
	}
	var d Diff
	for name, ts := range server {
		switch e, ok := recorded[name]; {
		case !ok:
			d.New = append(d.New, name)
		case e.ServerTS != ts:
			d.Changed = append(d.Changed, name)
		default:
			d.Unchanged = append(d.Unchanged, name)
		}
	}
	for name := range recorded {
		if _, ok := server[name]; !ok {
			d.Deleted = append(d.Deleted, name)
		}
	}
	sort.Strings(d.New)
	sort.Strings(d.Changed)
	sort.Strings(d.Deleted)
	sort.Strings(d.Unchanged)
	return d
}
