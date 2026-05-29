package lock

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAcquireReleaseRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".irissync-push.lock")
	l, err := Acquire(path, 15*time.Minute)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("lock file not created: %v", statErr)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("lock file should be gone after Release, stat err = %v", statErr)
	}
}

func TestAcquireHeldByLiveLockFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".irissync-push.lock")
	l1, err := Acquire(path, 15*time.Minute)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer func() { _ = l1.Release() }()

	// A second acquire against a lock held by this (live) PID must fail with a
	// Held error that carries the holder info.
	_, err = Acquire(path, 15*time.Minute)
	var he *HeldError
	if err == nil {
		t.Fatal("expected second Acquire to fail while the lock is held")
	}
	if !asHeld(err, &he) {
		t.Fatalf("expected *HeldError, got %T: %v", err, err)
	}
	if he.Holder.PID != os.Getpid() {
		t.Errorf("holder PID = %d, want %d", he.Holder.PID, os.Getpid())
	}
}

func TestStaleLockReclaimedByTTL(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".irissync-push.lock")
	// Write a lock file that is older than the TTL, owned by a PID that is
	// (almost certainly) not running, on a foreign host so the dead-PID check
	// does not short-circuit the TTL path.
	stale := Holder{Host: "some-other-host", PID: 999999, StartedAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)}
	if err := writeHolder(path, stale); err != nil {
		t.Fatalf("seed stale lock: %v", err)
	}
	l, err := Acquire(path, time.Minute) // TTL 1m, lock is 1h old → reclaimed
	if err != nil {
		t.Fatalf("expected stale lock to be reclaimed, got %v", err)
	}
	defer func() { _ = l.Release() }()
	if !l.Reclaimed() {
		t.Error("expected Reclaimed() to report a reclaim")
	}
}

func TestFreshLockNotReclaimedByTTL(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".irissync-push.lock")
	fresh := Holder{Host: "some-other-host", PID: 999999, StartedAt: time.Now().UTC().Format(time.RFC3339)}
	if err := writeHolder(path, fresh); err != nil {
		t.Fatalf("seed fresh lock: %v", err)
	}
	// Fresh foreign lock within TTL → must refuse (can't tell if alive).
	if _, err := Acquire(path, time.Hour); err == nil {
		t.Fatal("expected a fresh foreign lock within TTL to block acquisition")
	}
}

func TestCorruptLockFileIsReclaimed(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".irissync-push.lock")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := Acquire(path, time.Minute)
	if err != nil {
		t.Fatalf("corrupt lock should be reclaimable, got %v", err)
	}
	defer func() { _ = l.Release() }()
}

func TestHeldErrorMessage(t *testing.T) {
	he := &HeldError{Holder: Holder{Host: "h", PID: 42, StartedAt: "2026-05-28T00:00:00Z"}}
	if !strings.Contains(he.Error(), "42") || !strings.Contains(he.Error(), "h") {
		t.Errorf("HeldError message lacks holder info: %q", he.Error())
	}
}
