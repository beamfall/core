package store

import (
	"path/filepath"
	"testing"
)

func TestOpenSQLiteWriterSetsWALDurabilityPolicy(t *testing.T) {
	db, err := openSQLiteWriter(filepath.Join(t.TempDir(), "policy.db"))
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	defer db.Close()

	var synchronous int
	if err := db.QueryRow("PRAGMA synchronous").Scan(&synchronous); err != nil {
		t.Fatalf("synchronous: %v", err)
	}
	if synchronous != 2 {
		t.Fatalf("synchronous = %d, want FULL (2)", synchronous)
	}

	var autoCheckpoint int
	if err := db.QueryRow("PRAGMA wal_autocheckpoint").Scan(&autoCheckpoint); err != nil {
		t.Fatalf("wal_autocheckpoint: %v", err)
	}
	if autoCheckpoint != 1000 {
		t.Fatalf("wal_autocheckpoint = %d, want 1000", autoCheckpoint)
	}
}
