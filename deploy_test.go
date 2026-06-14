package main

import (
	"strings"
	"testing"
)

func TestDeployDocname(t *testing.T) {
	cases := map[string]string{
		"src/STDJSON.m":            "STDJSON.mac",
		"../m-stdlib/src/STDB64.m": "STDB64.mac",
		"stdregex.m":               "STDREGEX.mac", // routine names are upper-cased
		"/abs/path/STDCSV.m":       "STDCSV.mac",
	}
	for in, want := range cases {
		if got := deployDocname(in); got != want {
			t.Errorf("deployDocname(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCommonStemPrefix(t *testing.T) {
	cases := []struct {
		stems []string
		want  string
	}{
		{[]string{"STDJSON", "STDB64", "STDASSERT"}, "STD"},
		{[]string{"STDJSON"}, "STDJSON"},
		{[]string{"STDARGS", "STDASSERT"}, "STDA"},
		{[]string{"STDJSON", "DGREG"}, ""}, // no shared prefix
		{nil, ""},
	}
	for _, tc := range cases {
		if got := commonStemPrefix(tc.stems); got != tc.want {
			t.Errorf("commonStemPrefix(%v) = %q, want %q", tc.stems, got, tc.want)
		}
	}
}

func TestPrunePlan(t *testing.T) {
	deployed := []string{"STDJSON.mac", "STDB64.mac"}
	server := []string{
		"STDJSON.mac", // deployed → keep
		"STDB64.mac",  // deployed → keep
		"STDOLD.mac",  // STD-prefixed orphan → prune
		"DGREG.mac",   // VistA routine, not in prune scope → never touched
		"XUSER.mac",   // VistA routine → never touched
	}
	orphans, prefix, err := prunePlan(deployed, server)
	if err != nil {
		t.Fatalf("prunePlan err = %v", err)
	}
	if prefix != "STD" {
		t.Errorf("prefix = %q, want STD", prefix)
	}
	if len(orphans) != 1 || orphans[0] != "STDOLD.mac" {
		t.Fatalf("orphans = %v, want [STDOLD.mac]", orphans)
	}
}

func TestPrunePlanRefusesAmbiguousScope(t *testing.T) {
	// A deploy set with no coherent common prefix must NOT be allowed to prune —
	// otherwise the scope could widen to unrelated routines (e.g. VistA's).
	_, _, err := prunePlan([]string{"STDJSON.mac", "DGREG.mac"}, []string{"STDJSON.mac", "ANY.mac"})
	if err == nil {
		t.Fatal("expected prunePlan to refuse an ambiguous (too-short) prune prefix")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "prefix") {
		t.Errorf("error should explain the prefix guard, got: %v", err)
	}
}
