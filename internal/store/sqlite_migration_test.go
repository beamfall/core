package store

import (
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestMigrateUserVersionDoesNotRegressForOutOfOrderMigration(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "catalog.db")
	backupDir := filepath.Join(t.TempDir(), "backups")
	initial := fstest.MapFS{
		"migrations/0001_initial.sql": {Data: []byte("CREATE TABLE migration_one (id INTEGER PRIMARY KEY);")},
		"migrations/0003_later.sql":   {Data: []byte("CREATE TABLE migration_three (id INTEGER PRIMARY KEY);")},
		"migrations/0004_latest.sql":  {Data: []byte("CREATE TABLE migration_four (id INTEGER PRIMARY KEY);")},
	}

	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, slog.Default(), db, dbPath, "catalog", initial, "migrations", backupDir); err != nil {
		t.Fatalf("initial migrate: %v", err)
	}
	before := migrationUserVersion(t, db)
	if before != 3 {
		t.Fatalf("initial user_version = %d, want 3", before)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	withEarlierMigration := fstest.MapFS{
		"migrations/0001_initial.sql": {Data: []byte("CREATE TABLE migration_one (id INTEGER PRIMARY KEY);")},
		"migrations/0002_earlier.sql": {Data: []byte("CREATE TABLE migration_two (id INTEGER PRIMARY KEY);")},
		"migrations/0003_later.sql":   {Data: []byte("CREATE TABLE migration_three (id INTEGER PRIMARY KEY);")},
		"migrations/0004_latest.sql":  {Data: []byte("CREATE TABLE migration_four (id INTEGER PRIMARY KEY);")},
	}
	db, err = openSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrate(ctx, slog.Default(), db, dbPath, "catalog", withEarlierMigration, "migrations", backupDir); err != nil {
		t.Fatalf("migrate with earlier file: %v", err)
	}

	after := migrationUserVersion(t, db)
	if after < before {
		t.Fatalf("user_version regressed from %d to %d", before, after)
	}
	if after != 4 {
		t.Fatalf("user_version = %d, want applied-file count 4", after)
	}
	var recorded int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE name = ?", "0002_earlier.sql").Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if recorded != 1 {
		t.Fatalf("0002_earlier.sql records = %d, want 1", recorded)
	}
}

func TestMigrateUserVersionUnchangedForInOrderMigrations(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "catalog.db")
	backupDir := filepath.Join(t.TempDir(), "backups")
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	initial := fstest.MapFS{
		"migrations/0001_initial.sql": {Data: []byte("CREATE TABLE migration_one (id INTEGER PRIMARY KEY);")},
		"migrations/0002_second.sql":  {Data: []byte("CREATE TABLE migration_two (id INTEGER PRIMARY KEY);")},
	}
	if err := migrate(ctx, slog.Default(), db, dbPath, "catalog", initial, "migrations", backupDir); err != nil {
		t.Fatalf("initial migrate: %v", err)
	}
	if version := migrationUserVersion(t, db); version != 2 {
		t.Fatalf("initial user_version = %d, want 2", version)
	}

	withNextMigration := fstest.MapFS{
		"migrations/0001_initial.sql": {Data: []byte("CREATE TABLE migration_one (id INTEGER PRIMARY KEY);")},
		"migrations/0002_second.sql":  {Data: []byte("CREATE TABLE migration_two (id INTEGER PRIMARY KEY);")},
		"migrations/0003_third.sql":   {Data: []byte("CREATE TABLE migration_three (id INTEGER PRIMARY KEY);")},
	}
	if err := migrate(ctx, slog.Default(), db, dbPath, "catalog", withNextMigration, "migrations", backupDir); err != nil {
		t.Fatalf("migrate with next file: %v", err)
	}
	if version := migrationUserVersion(t, db); version != 3 {
		t.Fatalf("user_version = %d, want 3", version)
	}
}

func TestMigrateReportsRecordedMigrationMissingFromFilesystem(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "catalog.db")
	backupDir := filepath.Join(t.TempDir(), "backups")
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	initial := fstest.MapFS{
		"migrations/0001_initial.sql": {Data: []byte("CREATE TABLE migration_one (id INTEGER PRIMARY KEY);")},
		"migrations/0002_second.sql":  {Data: []byte("CREATE TABLE migration_two (id INTEGER PRIMARY KEY);")},
	}
	if err := migrate(ctx, slog.Default(), db, dbPath, "catalog", initial, "migrations", backupDir); err != nil {
		t.Fatalf("initial migrate: %v", err)
	}

	missingRecordedFile := fstest.MapFS{
		"migrations/0001_initial.sql": {Data: []byte("CREATE TABLE migration_one (id INTEGER PRIMARY KEY);")},
	}
	err = migrate(ctx, slog.Default(), db, dbPath, "catalog", missingRecordedFile, "migrations", backupDir)
	if err == nil {
		t.Fatal("migrate accepted a recorded migration missing from the filesystem")
	}
	const want = "catalog recorded migration 0002_second.sql is missing from the migration filesystem; migration files are append-only"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
}

func migrationUserVersion(t *testing.T, db *sql.DB) int {
	t.Helper()
	version, err := userVersion(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	return version
}
