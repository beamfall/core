package store

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Replaying the full catalog + events migration chain costs about 3.4 s per
// store under -race, and most tests here open at least one store. The template
// below is migrated once per test binary, lazily, and kept in memory; each
// caller of migratedDataDir gets its own temp data dir holding a byte copy of
// both databases. Open then finds the schema current (no migrations run) and
// seeds the copy exactly as it would a fresh one, so every test still owns a
// private, isolated store at the current schema.
//
// The template is unseeded on purpose: several seed profiles embed the data
// dir's own paths in their rows, so seeding stays per test.
//
// Tests that assert migration behaviour itself (sqlite_migration_test.go and
// friends) keep opening an empty t.TempDir() and replay the real chain.
var (
	migratedTemplateOnce    sync.Once
	migratedTemplateCatalog []byte
	migratedTemplateEvents  []byte
	migratedTemplateErr     error
)

// migratedDataDir returns a fresh data dir whose db/catalog.db and
// db/events.db are private copies of the migrated template.
func migratedDataDir(tb testing.TB) string {
	tb.Helper()
	migratedTemplateOnce.Do(func() {
		migratedTemplateCatalog, migratedTemplateEvents, migratedTemplateErr = buildMigratedTemplate()
	})
	if migratedTemplateErr != nil {
		tb.Fatalf("migrated template: %v", migratedTemplateErr)
	}
	dataDir := tb.TempDir()
	dbDir := filepath.Join(dataDir, "db")
	if err := os.MkdirAll(dbDir, 0o700); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dbDir, "catalog.db"), migratedTemplateCatalog, 0o600); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dbDir, "events.db"), migratedTemplateEvents, 0o600); err != nil {
		tb.Fatal(err)
	}
	return dataDir
}

func buildMigratedTemplate() (catalog, events []byte, err error) {
	dir, err := os.MkdirTemp("", "beamfall-store-template-")
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	st, err := Open(context.Background(), dir, Options{Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		return nil, nil, err
	}
	if err := st.Close(); err != nil {
		return nil, nil, err
	}
	dbDir := filepath.Join(dir, "db")
	// Closing the last connection checkpoints the WAL; a leftover sidecar would
	// mean the main files alone are not the whole database, so refuse.
	for _, name := range []string{"catalog.db-wal", "events.db-wal"} {
		if fi, statErr := os.Stat(filepath.Join(dbDir, name)); statErr == nil && fi.Size() > 0 {
			return nil, nil, fmt.Errorf("%s not checkpointed after Close (%d bytes)", name, fi.Size())
		}
	}
	root, err := os.OpenRoot(dbDir)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = root.Close() }()
	if catalog, err = root.ReadFile("catalog.db"); err != nil {
		return nil, nil, err
	}
	if events, err = root.ReadFile("events.db"); err != nil {
		return nil, nil, err
	}
	return catalog, events, nil
}

// TestMigratedDataDirIsPrivate pins the isolation contract: two template
// copies never share rows or schema.
func TestMigratedDataDirIsPrivate(t *testing.T) {
	ctx := context.Background()
	open := func() *Store {
		st, err := Open(ctx, migratedDataDir(t), Options{SeedDevelopmentData: true})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st.Close() })
		return st
	}
	a, b := open(), open()
	if _, err := a.catalogW.ExecContext(ctx,
		"CREATE TABLE template_isolation_probe (v INTEGER); INSERT INTO template_isolation_probe VALUES (1)"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := b.catalogW.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE name = 'template_isolation_probe'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("a write to one template copy is visible in another")
	}
}
