package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckSQLiteIntegrityHealthy is the no-false-positive guard for PANEL-10:
// a normal, healthy database must pass quick_check. (The real risk of a startup
// integrity check is bricking boot on a healthy DB, so this is the load-bearing
// assertion.)
func TestCheckSQLiteIntegrityHealthy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "healthy.db")
	db, err := openSQLite(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	for i := 0; i < 300; i++ {
		if _, err := db.Exec(`INSERT INTO t(v) VALUES (?)`, "row"); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	if err := checkSQLiteIntegrity(context.Background(), db, "catalog", path); err != nil {
		t.Fatalf("healthy DB flagged as corrupt: %v", err)
	}
	_ = db.Close()
}

// TestCheckSQLiteIntegrityDetectsCorruption crafts a structurally-damaged
// database (a b-tree page smashed with 0xFF) and asserts quick_check catches it
// and that Open surfaces a *DatabaseCorruptedError rather than starting.
func TestCheckSQLiteIntegrityDetectsCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corrupt.db")

	// Build a multi-page DB, then checkpoint so the rows live in the main file
	// (not only the WAL) before we corrupt it.
	db, err := openSQLite(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	for i := 0; i < 500; i++ {
		if _, err := db.Exec(`INSERT INTO t(v) VALUES (?)`, "a fairly long value to push past one page boundary"); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Smash a stretch of interior pages (leave page 1 / the header intact so the
	// file still opens, but corrupt the b-tree so quick_check fails).
	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("reopen file: %v", err)
	}
	info, err := f.Stat()
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() < 3*4096 {
		t.Fatalf("test DB unexpectedly small (%d bytes); needs multiple pages", info.Size())
	}
	garbage := make([]byte, 2*4096)
	for i := range garbage {
		garbage[i] = 0xFF
	}
	// Start at page 2 (offset 4096), past the page-1 header/schema.
	if _, err := f.WriteAt(garbage, 4096); err != nil {
		t.Fatalf("write garbage: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close file: %v", err)
	}

	// Direct helper check.
	cdb, err := openSQLite(path)
	if err == nil {
		if err := checkSQLiteIntegrity(context.Background(), cdb, "catalog", path); err == nil {
			t.Fatal("checkSQLiteIntegrity did not detect a smashed b-tree page")
		}
		_ = cdb.Close()
	}

	// End-to-end: Open must refuse with a *DatabaseCorruptedError. The corrupt
	// file is the catalog DB, so place it where Open looks.
	dataDir := t.TempDir()
	dbDir := filepath.Join(dataDir, "db")
	if err := os.MkdirAll(dbDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := copyFile(path, filepath.Join(dbDir, "catalog.db")); err != nil {
		t.Fatalf("seed corrupt catalog: %v", err)
	}
	_, openErr := Open(context.Background(), dataDir, Options{})
	if openErr == nil {
		t.Fatal("Open started on a corrupt catalog database")
	}
	var corruptErr *DatabaseCorruptedError
	if !errors.As(openErr, &corruptErr) {
		t.Fatalf("Open returned %T (%v), want *DatabaseCorruptedError", openErr, openErr)
	}
	if corruptErr.Name != "catalog" {
		t.Fatalf("corruption reported for %q, want catalog", corruptErr.Name)
	}
	if !strings.Contains(openErr.Error(), "/manage/backups#restore") {
		t.Fatalf("Open corruption error = %q, want guided restore route", openErr)
	}
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o600)
}
