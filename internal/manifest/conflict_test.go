package manifest

import "testing"

func TestServerChangedSincePull(t *testing.T) {
	m := New("i", "VISTA")
	m.Routines["DGREG.mac"] = Entry{ServerTS: "2026-05-20 09:14:22.000", SHA256: "abc", Bytes: 10}

	tests := []struct {
		name     string
		docname  string
		serverTS string
		exists   bool
		wantConf bool
		wantKind ConflictKind
	}{
		{"unchanged: same ts → no conflict", "DGREG.mac", "2026-05-20 09:14:22.000", true, false, ConflictNone},
		{"server edited: different ts → conflict", "DGREG.mac", "2026-05-25 00:00:00.000", true, true, ConflictChanged},
		{"server deleted: gone underneath us → conflict", "DGREG.mac", "", false, true, ConflictDeleted},
		{"new local routine (no manifest entry) → no conflict, creatable", "NEW.mac", "", false, false, ConflictNone},
		{"new local routine but server already has it → conflict", "NEW.mac", "2026-05-01 00:00:00.000", true, true, ConflictExists},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := CheckConflict(m, tt.docname, tt.serverTS, tt.exists)
			if (c.Kind != ConflictNone) != tt.wantConf {
				t.Errorf("conflict = %v (kind=%v), want %v", c.Kind != ConflictNone, c.Kind, tt.wantConf)
			}
			if c.Kind != tt.wantKind {
				t.Errorf("kind = %v, want %v", c.Kind, tt.wantKind)
			}
		})
	}
}

func TestConflictHasMessage(t *testing.T) {
	m := New("i", "VISTA")
	m.Routines["A.mac"] = Entry{ServerTS: "t1"}
	c := CheckConflict(m, "A.mac", "t2", true)
	if c.Kind == ConflictNone {
		t.Fatal("expected a conflict")
	}
	if c.Message == "" {
		t.Error("expected a non-empty conflict message")
	}
}
