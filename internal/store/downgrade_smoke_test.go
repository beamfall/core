package store

import (
	"context"
	"fmt"
	"testing"
)

// TestDowngradeSmokeRefusesCatalogNewerThanBinary verifies that opening a
// catalog database whose user_version exceeds the number of embedded
// migrations returns a non-nil error and does not panic.
//
// This simulates deploying an older binary against a database that was
// previously opened by a newer binary: the migration count is the binary's
// declared schema ceiling, so a DB version above it must be refused.
func TestDowngradeSmokeRefusesCatalogNewerThanBinary(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()

	st, err := Open(ctx, dataDir, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Bump catalog user_version to one beyond the current migration count.
	future := migrationFileCount(t, "migrate/catalog") + 1
	if _, err := st.catalogW.Exec(fmt.Sprintf("PRAGMA user_version = %d", future)); err != nil {
		t.Fatalf("set user_version: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	_, err = Open(ctx, dataDir, Options{})
	if err == nil {
		t.Fatal("Open accepted catalog schema newer than binary (expected error, got nil)")
	}
}

// TestDowngradeSmokeRefusesEventsNewerThanBinary is the same scenario for
// events.db: the binary must refuse to open an events database whose
// user_version exceeds the embedded migration count.
func TestDowngradeSmokeRefusesEventsNewerThanBinary(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()

	st, err := Open(ctx, dataDir, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Bump events user_version to one beyond the current migration count.
	future := migrationFileCount(t, "migrate/events") + 1
	if _, err := st.eventsW.Exec(fmt.Sprintf("PRAGMA user_version = %d", future)); err != nil {
		t.Fatalf("set user_version: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	_, err = Open(ctx, dataDir, Options{})
	if err == nil {
		t.Fatal("Open accepted events schema newer than binary (expected error, got nil)")
	}
}
