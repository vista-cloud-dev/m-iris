package main

import "testing"

// match's --filter glob is bare-name: the routine type extension (.mac/.int/.inc)
// is stripped before the glob is applied, matching m-ydb's source.Match and
// driver-contract §5.2. The --package prefix matches the full docname.
func TestMatchBareNameFilter(t *testing.T) {
	cases := []struct {
		docname, glob, pkg string
		want               bool
	}{
		{"DGREG.mac", "DG*", "", true},    // prefix glob on the bare name
		{"DGREG.mac", "DGREG", "", true},  // exact bare name (no extension)
		{"DGREG.mac", "*.mac", "", false}, // bare-name glob: ".mac" is stripped, never matches
		{"XUSER.mac", "DG*", "", false},   // non-matching prefix
		{"DGREG.int", "DG*", "", true},    // works across routine types
		{"DGREG.mac", "", "DG", true},     // package prefix on the docname
		{"XUSER.mac", "", "DG", false},    // package prefix excludes
		{"DGREG.mac", "DG*", "DG", true},  // both gates pass
		{"DGREG.mac", "", "", true},       // no filter matches everything
	}
	for _, c := range cases {
		got, err := match(c.docname, c.glob, c.pkg)
		if err != nil {
			t.Fatalf("match(%q,%q,%q): %v", c.docname, c.glob, c.pkg, err)
		}
		if got != c.want {
			t.Errorf("match(%q,%q,%q) = %v, want %v", c.docname, c.glob, c.pkg, got, c.want)
		}
	}
}
