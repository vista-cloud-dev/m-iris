// Package lock provides the single-writer push lock that serializes
// `irissync push` against one mirror/namespace. It is the narrowest of the
// three single-writer layers in liberation-binary-design §5 (the others — the
// manifest conflict-check and detect-and-defer — live in package manifest and
// the push command). The lock is a file created atomically with
// O_CREATE|O_EXCL holding {host, pid, startedAt}; a stale lock (a dead PID on
// this host, or one older than the TTL) is reclaimed with a warning.
//
// Implementation is stdlib-only (os, encoding/json) so it adds nothing to the
// SBOM surface.
package lock

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Holder records who owns a lock, for stale-detection and diagnostics.
type Holder struct {
	Host      string `json:"host"`
	PID       int    `json:"pid"`
	StartedAt string `json:"startedAt"` // RFC3339 UTC
}

// HeldError is returned by Acquire when a live lock is held by another writer.
// The push command maps it to exit 4 (refused) — never clobber a concurrent
// writer.
type HeldError struct {
	Holder Holder
	Path   string
}

func (e *HeldError) Error() string {
	return fmt.Sprintf("push lock held by host=%s pid=%d since %s (%s)",
		e.Holder.Host, e.Holder.PID, e.Holder.StartedAt, e.Path)
}

// asHeld is a small errors.As helper used by tests.
func asHeld(err error, target **HeldError) bool { return errors.As(err, target) }

// Lock is a held push lock. Call Release when done (defer it).
type Lock struct {
	path      string
	reclaimed bool
}

// Reclaimed reports whether acquiring this lock reclaimed a stale one (so the
// caller can warn).
func (l *Lock) Reclaimed() bool { return l.reclaimed }

// Acquire takes the exclusive push lock at path. It creates the lock file
// atomically (O_CREATE|O_EXCL). If the file already exists it inspects the
// holder: a lock owned by a dead PID on this host, older than ttl, or
// unparseable is reclaimed; otherwise Acquire returns a *HeldError. ttl<=0
// disables TTL-based reclaim (only dead-PID reclaim applies).
func Acquire(path string, ttl time.Duration) (*Lock, error) {
	reclaimed, err := tryCreate(path)
	if err == nil {
		return &Lock{path: path}, nil
	}
	if !errors.Is(err, os.ErrExist) {
		return nil, err
	}

	// Lock file exists — decide whether it is stale.
	holder, ok := readHolder(path)
	stale := !ok || isStale(holder, ttl)
	if !stale {
		return nil, &HeldError{Holder: holder, Path: path}
	}

	// Reclaim: remove the stale file and retry the exclusive create once. If we
	// lose the race (another process recreated it), report it as held.
	if rmErr := os.Remove(path); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
		return nil, fmt.Errorf("lock: reclaim stale lock %s: %w", path, rmErr)
	}
	if _, err := tryCreate(path); err != nil {
		if errors.Is(err, os.ErrExist) {
			holder, _ := readHolder(path)
			return nil, &HeldError{Holder: holder, Path: path}
		}
		return nil, err
	}
	_ = reclaimed
	return &Lock{path: path, reclaimed: true}, nil
}

// Release removes the lock file. It is safe to call once.
func (l *Lock) Release() error {
	if l == nil || l.path == "" {
		return nil
	}
	err := os.Remove(l.path)
	l.path = ""
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// tryCreate atomically creates the lock file with the current holder. It
// returns os.ErrExist (wrapped) if the file already exists.
func tryCreate(path string) (Holder, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Holder{}, err
	}
	h := self()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return Holder{}, err
	}
	data, mErr := json.MarshalIndent(h, "", "  ")
	if mErr != nil {
		f.Close()
		os.Remove(path)
		return Holder{}, mErr
	}
	if _, wErr := f.Write(append(data, '\n')); wErr != nil {
		f.Close()
		os.Remove(path)
		return Holder{}, wErr
	}
	if cErr := f.Close(); cErr != nil {
		os.Remove(path)
		return Holder{}, cErr
	}
	return h, nil
}

// writeHolder writes an arbitrary holder to path (used by tests to seed a lock).
func writeHolder(path string, h Holder) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func readHolder(path string) (Holder, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Holder{}, false
	}
	var h Holder
	if err := json.Unmarshal(data, &h); err != nil {
		return Holder{}, false
	}
	return h, true
}

// isStale reports whether a held lock can be reclaimed: same-host dead PID, or
// older than ttl.
func isStale(h Holder, ttl time.Duration) bool {
	host, _ := os.Hostname()
	if h.Host == host && h.PID > 0 && !pidAlive(h.PID) {
		return true
	}
	if ttl > 0 && h.StartedAt != "" {
		if started, err := time.Parse(time.RFC3339, h.StartedAt); err == nil {
			if time.Since(started) > ttl {
				return true
			}
		} else {
			return true // unparseable timestamp → treat as stale
		}
	}
	return false
}

// pidAlive reports whether a process with pid exists on this host. On Unix a
// signal-0 to the process tests for existence.
func pidAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	// EPERM means the process exists but we can't signal it → alive.
	return errors.Is(err, syscall.EPERM)
}

func self() Holder {
	host, _ := os.Hostname()
	return Holder{Host: host, PID: os.Getpid(), StartedAt: time.Now().UTC().Format(time.RFC3339)}
}
