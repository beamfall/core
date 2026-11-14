package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOpenResolvesRelativeDataDir covers CORE-1599: a relative data dir used to
// reach SQLite as a file: URI authority ("invalid uri authority").
func TestOpenResolvesRelativeDataDir(t *testing.T) {
	parent := t.TempDir()
	t.Chdir(parent)
	st, err := Open(context.Background(), "data-a", Options{})
	if err != nil {
		t.Fatalf("Open relative data dir: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(parent, "data-a", "db", "catalog.db")); err != nil {
		t.Fatalf("catalog not created under the working directory: %v", err)
	}
	st, err = Open(context.Background(), "data-a", Options{RequireExisting: true})
	if err != nil {
		t.Fatalf("Open existing relative data dir: %v", err)
	}
	_ = st.Close()
}

// TestOpenRequireExistingRefusesAndCreatesNothing covers CORE-1599: offline
// commands pointed at a wrong path must not silently create a new server.
func TestOpenRequireExistingRefusesAndCreatesNothing(t *testing.T) {
	parent := t.TempDir()
	for _, dataDir := range []string{
		filepath.Join(parent, "typo"),
		parent, // exists, but holds no database
	} {
		_, err := Open(context.Background(), dataDir, Options{RequireExisting: true})
		if !errors.Is(err, ErrNoDataDir) || !strings.Contains(err.Error(), dataDir) {
			t.Fatalf("Open(%s) error = %v, want ErrNoDataDir naming the path", dataDir, err)
		}
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("RequireExisting created %d entries in %s", len(entries), parent)
	}
}

// TestOpenFreshDataDirLogsOneMigrationLine covers CORE-1603: a fresh install
// used to log one INFO line per migration, burying the first-run setup URL.
func TestOpenFreshDataDirLogsOneMigrationLine(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	st, err := Open(context.Background(), t.TempDir(), Options{Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var migrationLines []string
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.Contains(line, "migration") {
			migrationLines = append(migrationLines, line)
		}
	}
	if len(migrationLines) != 1 || !strings.Contains(migrationLines[0], `msg="applied migrations"`) {
		t.Fatalf("INFO migration lines = %q, want one applied migrations summary", migrationLines)
	}
}

// TestOpenRequireExistingRefusesForeignCatalog covers CORE-1599: a catalog.db
// that is empty, is not SQLite, or is some other SQLite database is not
// Beamfall data, and refusing it must leave every byte on disk as it was.
func TestOpenRequireExistingRefusesForeignCatalog(t *testing.T) {
	foreignSQLite := filepath.Join(t.TempDir(), "foreign.db")
	db, err := sql.Open("sqlite", foreignSQLite)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE notes (body TEXT); INSERT INTO notes VALUES ('hello')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	foreignBytes, err := os.ReadFile(foreignSQLite) // #nosec G304 -- path is beneath a test-owned temporary directory
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string][]byte{
		"empty":          nil,
		"text":           []byte("not a database\n"),
		"foreign sqlite": foreignBytes,
	} {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			catalog := filepath.Join(dataDir, "db", "catalog.db")
			if err := os.MkdirAll(filepath.Dir(catalog), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(catalog, content, 0o600); err != nil {
				t.Fatal(err)
			}
			before := snapshotTree(t, dataDir)
			_, err := Open(context.Background(), dataDir, Options{RequireExisting: true})
			if !errors.Is(err, ErrNoDataDir) || !strings.Contains(err.Error(), "not a Beamfall catalog") {
				t.Fatalf("Open error = %v, want ErrNoDataDir naming a foreign catalog", err)
			}
			if after := snapshotTree(t, dataDir); after != before {
				t.Fatalf("refused Open changed the data dir:\nbefore:\n%s\nafter:\n%s", before, after)
			}
		})
	}
}

// snapshotTree lists every path under root with its size and content hash.
func snapshotTree(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			fmt.Fprintf(&b, "%s/\n", rel)
			return nil
		}
		data, err := os.ReadFile(path) // #nosec G304 G122 -- path is walked beneath a test-owned temporary directory
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "%s %d %x\n", rel, len(data), sha256.Sum256(data))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}
