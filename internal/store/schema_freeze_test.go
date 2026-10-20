package store

import (
	"encoding/json"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

// TestSchemaFreeze is the ADR-0011 §2 / FREEZE-4 freeze marker's enforcement.
// It counts the embedded migrations the same way the runner does (one .sql file
// per migration) and asserts the count matches the frozen baseline pinned in
// schema_freeze.go. Adding a migration past the freeze trips this test on
// purpose: the schema cannot move past its last benchmarked shape without a
// deliberate marker bump (and the `make bench` re-run that bump documents).
func TestSchemaFreeze(t *testing.T) {
	cases := []struct {
		name      string
		fsys      fs.FS
		dir       string
		want      int
		constName string
	}{
		{"catalog", catalogMigrations, "migrate/catalog", schemaFreezeCatalogMigrations, "schemaFreezeCatalogMigrations"},
		{"events", eventsMigrations, "migrate/events", schemaFreezeEventsMigrations, "schemaFreezeEventsMigrations"},
		{"encrypted_library", encryptedLibraryMigrations, "migrate/encrypted_library", schemaFreezeEncryptedLibraryMigrations, "schemaFreezeEncryptedLibraryMigrations"},
	}
	for _, tc := range cases {
		entries, err := fs.ReadDir(tc.fsys, tc.dir)
		if err != nil {
			t.Fatalf("%s: read embedded migrations: %v", tc.name, err)
		}
		got := 0
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
				got++
			}
		}
		if got != tc.want {
			t.Fatalf("%s schema is FROZEN (ADR-0011 §2 / FREEZE-4): found %d migrations, "+
				"marker pins %d. A schema change past the freeze is deliberate — run "+
				"`make bench`, confirm the P95 budgets still hold, then bump "+
				"%s in schema_freeze.go in the same commit.",
				tc.name, got, tc.want, tc.constName)
		}
	}
}

func TestFrozenSchemaRejectsDestructiveMigrations(t *testing.T) {
	base := fstest.MapFS{
		"internal/store/migrate/catalog/0001_initial.sql": {
			Data: []byte("CREATE TABLE item (id TEXT PRIMARY KEY, title TEXT NOT NULL);"),
		},
	}
	want, err := captureFrozenSchema(base)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}

	for name, migration := range map[string]string{
		"dropped column": "ALTER TABLE item DROP COLUMN title;",
		"retyped column": "CREATE TABLE replacement (id TEXT PRIMARY KEY, title INTEGER NOT NULL); DROP TABLE item; ALTER TABLE replacement RENAME TO item;",
	} {
		t.Run(name, func(t *testing.T) {
			fixture := cloneMigrationFS(base)
			fixture["internal/store/migrate/catalog/0002_destructive.sql"] = &fstest.MapFile{Data: []byte(migration)}
			if err := VerifyFrozenSchema(fixture, manifest); err == nil {
				t.Fatal("destructive migration passed the frozen schema gate")
			}
		})
	}

	rewritten := cloneMigrationFS(base)
	rewritten["internal/store/migrate/catalog/0001_initial.sql"] = &fstest.MapFile{
		Data: []byte("CREATE TABLE item (id TEXT PRIMARY KEY, title TEXT NOT NULL); -- rewritten"),
	}
	if err := VerifyFrozenSchema(rewritten, manifest); err == nil {
		t.Fatal("rewritten migration history passed the frozen schema gate")
	}
}

func cloneMigrationFS(source fstest.MapFS) fstest.MapFS {
	clone := make(fstest.MapFS, len(source)+1)
	for name, file := range source {
		clone[name] = file
	}
	return clone
}
