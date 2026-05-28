// Package mirror writes and reads the on-disk .mac mirror that irissync
// materializes from IRIS. The mirror is the input to file-based M tooling
// (m-cli's FilesystemSourceProvider), so writes are atomic, line endings are
// normalized, and the XML export wrapper is refused — the tree must stay
// git-stable and tree-sitter-parseable (liberation-binary-design §2.1).
package mirror

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ManifestName is the per-namespace manifest filename.
const ManifestName = ".irissync-manifest.json"

// Layout resolves paths within a mirror root for one instance/namespace:
//
//	<root>/<instance>/<namespace>/<ROUTINE>.mac
//	<root>/<instance>/<namespace>/.irissync-manifest.json
//
// NOTE: liberation-binary-design §2.1 illustrates an extra <package> path
// segment. Deriving a VistA package from a bare routine name requires the
// package-prefix map (a domain concern owned by vista-meta), which the read
// gate does not have, so routines are written flat under the namespace for now.
// The manifest (keyed by full docname) remains the source of truth either way.
type Layout struct {
	Root      string
	Instance  string
	Namespace string
}

// NamespaceDir is <root>/<instance>/<namespace>.
func (l Layout) NamespaceDir() string {
	return filepath.Join(l.Root, l.Instance, l.Namespace)
}

// ManifestPath is the namespace manifest file path.
func (l Layout) ManifestPath() string {
	return filepath.Join(l.NamespaceDir(), ManifestName)
}

// RoutinePath is the on-disk path for a routine docname (e.g. "DGREG.mac").
func (l Layout) RoutinePath(docname string) string {
	return filepath.Join(l.NamespaceDir(), docname)
}

// WriteResult reports what WriteRoutine persisted.
type WriteResult struct {
	SHA256 string
	Bytes  int
}

// WriteRoutine writes a routine's source to path atomically (temp file in the
// same directory + rename). Line endings are normalized to "\n" with a single
// trailing newline, and content that looks like an XML $SYSTEM.OBJ.Export
// wrapper is refused.
func WriteRoutine(path string, lines []string) (WriteResult, error) {
	body := normalize(lines)
	if err := guardUDL(body); err != nil {
		return WriteResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return WriteResult{}, err
	}
	if err := atomicWrite(path, body); err != nil {
		return WriteResult{}, err
	}
	sum := sha256.Sum256(body)
	return WriteResult{SHA256: hex.EncodeToString(sum[:]), Bytes: len(body)}, nil
}

// HashFile returns the sha256 (hex) and byte length of an existing file.
func HashFile(path string) (sum string, n int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	written, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), int(written), nil
}

// normalize joins server line-array content with "\n" and a trailing newline,
// stripping any stray CR/LF a line carries.
func normalize(lines []string) []byte {
	var b strings.Builder
	for _, ln := range lines {
		b.WriteString(strings.TrimRight(ln, "\r\n"))
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// guardUDL refuses the XML export wrapper; the mirror is plain UDL/.mac only.
func guardUDL(body []byte) error {
	trimmed := bytes.TrimPrefix(body, []byte("\uFEFF")) // strip a leading UTF-8 BOM
	trimmed = bytes.TrimLeft(trimmed, " \t\r\n")
	if bytes.HasPrefix(trimmed, []byte("<?xml")) ||
		bytes.HasPrefix(bytes.ToUpper(trimmed), []byte("<EXPORT")) {
		return errors.New("mirror: refusing XML export wrapper; expected plain UDL/Atelier .mac")
	}
	return nil
}

// atomicWrite writes data to a temp file in path's directory, then renames it
// over path so a reader never sees a partial file.
func atomicWrite(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".irissync-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
