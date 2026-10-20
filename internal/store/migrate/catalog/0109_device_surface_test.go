package catalog_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/store"
	_ "modernc.org/sqlite"
)

func TestDeviceSurfaceMigrationAppliesFromPreviousHead(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	dbDir := filepath.Join(dataDir, "db")
	if err := os.MkdirAll(dbDir, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dbDir, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE schema_migrations (
		name TEXT PRIMARY KEY,
		checksum TEXT NOT NULL,
		applied_at INTEGER NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	applied := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".sql") || name >= "0109_device_surface.sql" {
			continue
		}
		body, err := os.ReadFile(name) //nolint:gosec // package-local migration fixture
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, string(body)); err != nil {
			t.Fatalf("apply previous-head migration %s: %v", name, err)
		}
		sum := sha256.Sum256(body)
		if _, err := db.ExecContext(ctx,
			"INSERT INTO schema_migrations(name, checksum, applied_at) VALUES (?, ?, 0)",
			name, hex.EncodeToString(sum[:])); err != nil {
			t.Fatal(err)
		}
		applied++
	}
	if applied == 0 {
		t.Fatal("previous-head fixture applied zero migrations")
	}
	if _, err := db.ExecContext(ctx, "PRAGMA user_version = 104"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO device(id, name, role, distribution_class, secret_hash, revoked)
		VALUES ('device-before-0109', 'Previous head', 'viewer', 'direct', 'hash', 0)
	`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(ctx, dataDir, store.Options{})
	if err != nil {
		t.Fatalf("open previous-head catalog through current migrations: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	device, err := st.Device(ctx, "device-before-0109")
	if err != nil {
		t.Fatal(err)
	}
	if device.Surface != store.DeviceSurfaceCoreWebUI {
		t.Fatalf("backfilled device surface = %q, want %q", device.Surface, store.DeviceSurfaceCoreWebUI)
	}
}
