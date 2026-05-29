package manifest

import "fmt"

// ConflictKind classifies how the server's current state of a routine relates
// to the state captured in the manifest at pull time. Any kind other than
// ConflictNone means another writer touched the server since we pulled, so a
// push would clobber that change — push refuses (exit 4) unless --force. This
// is the cross-writer guard of liberation-binary-design §5 (layer 2).
type ConflictKind int

const (
	// ConflictNone — safe to push: the server matches the manifest (an
	// unchanged update), or the routine is new locally and absent on the server
	// (a clean create).
	ConflictNone ConflictKind = iota
	// ConflictChanged — the server timestamp differs from the pulled one: an
	// out-of-band edit.
	ConflictChanged
	// ConflictDeleted — we recorded the routine at pull, but it is gone from the
	// server now.
	ConflictDeleted
	// ConflictExists — a routine we have no manifest entry for already exists on
	// the server: pushing would overwrite a routine we never pulled.
	ConflictExists
)

func (k ConflictKind) String() string {
	switch k {
	case ConflictChanged:
		return "changed-on-server"
	case ConflictDeleted:
		return "deleted-on-server"
	case ConflictExists:
		return "exists-on-server"
	default:
		return "none"
	}
}

// Conflict is the result of a conflict-check for one routine.
type Conflict struct {
	Docname string
	Kind    ConflictKind
	Message string
}

// CheckConflict compares the server's current state of docname (its timestamp,
// and whether it exists at all) against the manifest entry recorded at the last
// pull. serverTS is the live server timestamp (empty if the routine does not
// exist); exists reports whether the server currently has the routine.
//
//   - manifest entry present + server matches  → ConflictNone (safe update)
//   - manifest entry present + server differs   → ConflictChanged
//   - manifest entry present + server gone       → ConflictDeleted
//   - no manifest entry      + server absent     → ConflictNone (clean create)
//   - no manifest entry      + server present    → ConflictExists
func CheckConflict(m *Manifest, docname, serverTS string, exists bool) Conflict {
	var entry Entry
	var recorded bool
	if m != nil {
		entry, recorded = m.Routines[docname]
	}

	switch {
	case recorded && !exists:
		return Conflict{docname, ConflictDeleted,
			fmt.Sprintf("%s was deleted on the server since pull (had ts %q)", docname, entry.ServerTS)}
	case recorded && serverTS != entry.ServerTS:
		return Conflict{docname, ConflictChanged,
			fmt.Sprintf("%s changed on the server since pull (pulled ts %q, now %q)", docname, entry.ServerTS, serverTS)}
	case !recorded && exists:
		return Conflict{docname, ConflictExists,
			fmt.Sprintf("%s already exists on the server but is not in the manifest (never pulled)", docname)}
	default:
		return Conflict{docname, ConflictNone, ""}
	}
}
