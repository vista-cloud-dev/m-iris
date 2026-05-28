package mirror

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteRoutineAtomicAndNormalized(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "DGREG.mac")

	wr, err := WriteRoutine(path, []string{"DGREG ;reg\r", "  q"})
	if err != nil {
		t.Fatalf("WriteRoutine: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "DGREG ;reg\n  q\n" // CR stripped, trailing newline added
	if string(got) != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
	if wr.Bytes != len(want) {
		t.Errorf("Bytes = %d, want %d", wr.Bytes, len(want))
	}

	// HashFile of what we wrote must equal the reported sum.
	sum, n, err := HashFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if sum != wr.SHA256 || n != wr.Bytes {
		t.Errorf("HashFile = (%s,%d), want (%s,%d)", sum, n, wr.SHA256, wr.Bytes)
	}

	// No leftover temp files in the directory.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("expected exactly one file, got %d", len(entries))
	}
}

func TestWriteRoutineRefusesXML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "BAD.mac")
	if _, err := WriteRoutine(path, []string{`<?xml version="1.0"?>`, `<Export generator="IRIS">`}); err == nil {
		t.Fatal("expected XML export wrapper to be refused")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("file should not have been written")
	}
}

func TestLayoutPaths(t *testing.T) {
	l := Layout{Root: ".m-cache", Instance: "vehu-dev", Namespace: "VISTA"}
	if got := l.RoutinePath("DGREG.mac"); got != filepath.Join(".m-cache", "vehu-dev", "VISTA", "DGREG.mac") {
		t.Errorf("RoutinePath = %q", got)
	}
	if got := l.ManifestPath(); got != filepath.Join(".m-cache", "vehu-dev", "VISTA", ManifestName) {
		t.Errorf("ManifestPath = %q", got)
	}
}
