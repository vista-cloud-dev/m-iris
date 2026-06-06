package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPushFromDir proves `push --from DIR` pushes routines from an arbitrary
// directory (not the mirror), landing them on the instance + mirror + manifest,
// including a routine the instance has never seen (a fresh create).
func TestPushFromDir(t *testing.T) {
	fake := newRWAtelier(map[string][]string{}, map[string]string{})
	srv := fake.start()
	defer srv.Close()

	conn := pullThenConn(t, srv) // empty server → empty mirror + manifest
	layout := conn.Layout()

	from := t.TempDir()
	if err := os.WriteFile(filepath.Join(from, "NEW.mac"), []byte("NEW ;fresh\n q\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Dry run: reports the plan, writes nothing.
	cc, buf := jsonCtx()
	if err := (&pushCmd{From: from}).Run(cc, withDryRun(conn)); err != nil {
		t.Fatalf("dry-run push --from: %v", err)
	}
	var dry struct {
		Data pushResult `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &dry); err != nil {
		t.Fatalf("decode: %v\n%s", err, buf.String())
	}
	if !dry.Data.DryRun || len(dry.Data.Items) != 1 {
		t.Errorf("dry run: dryRun=%v items=%v", dry.Data.DryRun, dry.Data.Items)
	}
	if len(fake.puts) != 0 {
		t.Errorf("dry run must not PUT, got %v", fake.puts)
	}
	if _, err := os.Stat(layout.RoutinePath("NEW.mac")); !os.IsNotExist(err) {
		t.Errorf("dry run must not stage the mirror file, err=%v", err)
	}

	// Real push --from.
	cc, buf = jsonCtx()
	if err := (&pushCmd{From: from}).Run(cc, conn); err != nil {
		t.Fatalf("push --from: %v", err)
	}
	var env struct {
		Data pushResult `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v\n%s", err, buf.String())
	}
	if env.Data.Pushed != 1 {
		t.Errorf("pushed = %d, want 1", env.Data.Pushed)
	}
	if got := strings.Join(fake.content["NEW.mac"], "\n"); got != "NEW ;fresh\n q" {
		t.Errorf("instance content after push --from = %q", got)
	}
	// Mirror staged + manifest updated, so a follow-up verify is clean.
	if _, err := os.Stat(layout.RoutinePath("NEW.mac")); err != nil {
		t.Errorf("mirror file not staged: %v", err)
	}
	cc, _ = jsonCtx()
	if err := (verifyCmd{}).Run(cc, conn); err != nil {
		t.Fatalf("verify after push --from: %v (want clean)", err)
	}
}
