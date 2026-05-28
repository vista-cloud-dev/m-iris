// Package manifest models .irissync-manifest.json — the per-namespace record
// that makes the mirror an incremental cache (pull fetches only new/changed)
// and a verifiable artifact (verify re-hashes against it). It is also the
// conflict-check basis for push (stage 2.1).
package manifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Schema is the manifest format version.
const Schema = 1

// Entry records one routine's last-pulled server state and content hash.
type Entry struct {
	ServerTS string `json:"serverTS"`
	SHA256   string `json:"sha256"`
	Bytes    int    `json:"bytes"`
}

// Manifest is the full per-namespace record. Routines is keyed by docname
// (e.g. "DGREG.mac").
type Manifest struct {
	Schema    int              `json:"schema"`
	Instance  string           `json:"instance"`
	Namespace string           `json:"namespace"`
	PulledAt  string           `json:"pulledAt"`
	Routines  map[string]Entry `json:"routines"`
}

// New returns an empty manifest for an instance/namespace.
func New(instance, namespace string) *Manifest {
	return &Manifest{
		Schema:    Schema,
		Instance:  instance,
		Namespace: namespace,
		Routines:  map[string]Entry{},
	}
}

// Load reads a manifest from path. A missing file is not an error: it returns
// (nil, nil) so callers can treat "never pulled" distinctly from a parse error.
func Load(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("manifest: parse %s: %w", path, err)
	}
	if m.Routines == nil {
		m.Routines = map[string]Entry{}
	}
	return &m, nil
}

// Save writes the manifest to path atomically. Map keys marshal in sorted order,
// so the file is deterministic and diffs cleanly in git.
func Save(path string, m *Manifest) error {
	if m.Schema == 0 {
		m.Schema = Schema
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".irissync-manifest-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
