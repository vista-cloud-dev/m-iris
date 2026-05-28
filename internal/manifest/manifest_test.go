package manifest

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadMissingIsNotError(t *testing.T) {
	m, err := Load(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	if m != nil {
		t.Fatalf("expected nil manifest for missing file, got %+v", m)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ns", ".irissync-manifest.json")
	m := New("vehu-dev", "VISTA")
	m.PulledAt = "2026-05-27T00:00:00Z"
	m.Routines["DGREG.mac"] = Entry{ServerTS: "2026-05-20 09:14:22.000", SHA256: "abc", Bytes: 10}

	if err := Save(path, m); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, m) {
		t.Fatalf("round-trip mismatch:\n got %+v\nwant %+v", got, m)
	}
}

func TestCompare(t *testing.T) {
	m := New("i", "VISTA")
	m.Routines = map[string]Entry{
		"SAME.mac":    {ServerTS: "t1"},
		"CHANGED.mac": {ServerTS: "t1"},
		"GONE.mac":    {ServerTS: "t1"},
	}
	server := map[string]string{
		"SAME.mac":    "t1",
		"CHANGED.mac": "t2",
		"NEW.mac":     "t9",
	}
	d := Compare(server, m)

	if !reflect.DeepEqual(d.New, []string{"NEW.mac"}) {
		t.Errorf("New = %v", d.New)
	}
	if !reflect.DeepEqual(d.Changed, []string{"CHANGED.mac"}) {
		t.Errorf("Changed = %v", d.Changed)
	}
	if !reflect.DeepEqual(d.Deleted, []string{"GONE.mac"}) {
		t.Errorf("Deleted = %v", d.Deleted)
	}
	if !reflect.DeepEqual(d.Unchanged, []string{"SAME.mac"}) {
		t.Errorf("Unchanged = %v", d.Unchanged)
	}
	if !d.Drift() {
		t.Error("expected drift")
	}
	if !reflect.DeepEqual(d.ToPull(), []string{"CHANGED.mac", "NEW.mac"}) {
		t.Errorf("ToPull = %v", d.ToPull())
	}
}

func TestCompareNilManifestAllNew(t *testing.T) {
	d := Compare(map[string]string{"A.mac": "t"}, nil)
	if !reflect.DeepEqual(d.New, []string{"A.mac"}) {
		t.Errorf("New = %v", d.New)
	}
	if d.Drift() != true {
		t.Error("expected drift for fresh pull")
	}
}
