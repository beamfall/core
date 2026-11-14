package store

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
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
// Beamfall data, and refusing it must leave every byte on disk as it was. A
// schema_migrations table alone does not make a Beamfall catalog: it must
// record the first catalog migration with the checksum the migrator records.
func TestOpenRequireExistingRefusesForeignCatalog(t *testing.T) {
	foreignBytes := sqliteFixture(t, `CREATE TABLE notes (body TEXT); INSERT INTO notes VALUES ('hello')`)
	foreignLedger := sqliteFixture(t, `CREATE TABLE schema_migrations (version INTEGER); INSERT INTO schema_migrations VALUES (1), (2)`)
	wrongChecksum := sqliteFixture(t, `
		CREATE TABLE schema_migrations (name TEXT PRIMARY KEY, checksum TEXT NOT NULL, applied_at INTEGER NOT NULL);
		INSERT INTO schema_migrations VALUES ('0001_initial.sql', 'not-the-beamfall-checksum', 0)`)
	for name, content := range map[string][]byte{
		"empty":                     nil,
		"text":                      []byte("not a database\n"),
		"foreign sqlite":            foreignBytes,
		"foreign schema_migrations": foreignLedger,
		"wrong first checksum":      wrongChecksum,
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

// sqliteFixture returns the bytes of a closed SQLite database built by sqlText.
func sqliteFixture(t *testing.T, sqlText string) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(sqlText); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path) // #nosec G304 -- path is beneath a test-owned temporary directory
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestOpenRequireExistingRefusesForeignWALDatabase covers CORE-1599: probing a
// WAL-mode database used to create its -wal and -shm before refusing it. The
// probe must leave the directory listing and every byte unchanged, whether the
// database is closed or another process holds it open with data in its WAL.
func TestOpenRequireExistingRefusesForeignWALDatabase(t *testing.T) {
	for _, holdOpen := range []bool{false, true} {
		t.Run(fmt.Sprintf("held open %v", holdOpen), func(t *testing.T) {
			dataDir := t.TempDir()
			catalog := filepath.Join(dataDir, "db", "catalog.db")
			if err := os.MkdirAll(filepath.Dir(catalog), 0o700); err != nil {
				t.Fatal(err)
			}
			if holdOpen {
				release := holdInOtherProcess(t, "wal", catalog)
				defer release()
			} else {
				db, err := sql.Open("sqlite", catalog)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(foreignWALFixtureSQL); err != nil {
					t.Fatal(err)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
			}
			before := snapshotTree(t, dataDir)
			if holdOpen != strings.Contains(before, "catalog.db-wal") {
				t.Fatalf("fixture -wal presence is not %v:\n%s", holdOpen, before)
			}
			_, err := Open(context.Background(), dataDir, Options{RequireExisting: true})
			if !errors.Is(err, ErrNoDataDir) {
				t.Fatalf("Open error = %v, want ErrNoDataDir", err)
			}
			if after := snapshotTree(t, dataDir); after != before {
				t.Fatalf("refused Open changed the data dir:\nbefore:\n%s\nafter:\n%s", before, after)
			}
		})
	}
}

const (
	foreignWALFixtureSQL = `PRAGMA journal_mode = WAL; PRAGMA wal_autocheckpoint = 0;
		CREATE TABLE notes (body TEXT); INSERT INTO notes VALUES ('hello')`
	holdDatabaseEnv = "BEAMFALL_TEST_HOLD_DATABASE"
)

// holdInOtherProcess keeps a database open in a child process until the
// returned release is called: kind "wal" builds a foreign WAL-mode database at
// path with its data still in the WAL, and kind "store" opens a Beamfall store
// on the data dir path. A second process matters: SQLite shares one -shm
// mapping between the connections of a process.
func holdInOtherProcess(t *testing.T, kind, path string) (release func()) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperHoldDatabase$") // #nosec G204 G702 -- re-runs this test binary
	cmd.Env = append(os.Environ(), holdDatabaseEnv+"="+kind+":"+path)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	release = func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "ready\n" {
		release()
		t.Fatalf("holder process said %q, %v", line, err)
	}
	return release
}

// TestHelperHoldDatabase is the child process of holdInOtherProcess; it does
// nothing in a normal test run.
func TestHelperHoldDatabase(t *testing.T) {
	kind, path, ok := strings.Cut(os.Getenv(holdDatabaseEnv), ":")
	switch {
	case !ok:
		return
	case kind == "store":
		st, err := Open(context.Background(), path, Options{Logger: slog.New(slog.DiscardHandler)})
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
	default:
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		defer db.Close()
		if _, err := db.Exec(foreignWALFixtureSQL); err != nil {
			t.Fatal(err)
		}
	}
	fmt.Println("ready")
	_, _ = io.Copy(io.Discard, os.Stdin)
}

// TestRequireExistingAcceptsCatalogOfRunningServer covers CORE-1599: a server
// started on a fresh data dir keeps its whole migration ledger in the catalog
// WAL until a checkpoint, and an offline command run beside it must still
// recognise the catalog without changing any of its files.
func TestRequireExistingAcceptsCatalogOfRunningServer(t *testing.T) {
	dataDir := t.TempDir()
	release := holdInOtherProcess(t, "store", dataDir)
	defer release()
	dbDir := filepath.Join(dataDir, "db")
	before := snapshotTree(t, dbDir)
	if !strings.Contains(before, "catalog.db-wal") {
		t.Fatalf("running server holds no catalog -wal:\n%s", before)
	}
	if err := requireExistingDataDir(context.Background(), dataDir); err != nil {
		t.Fatalf("requireExistingDataDir on a running server's data dir: %v", err)
	}
	if after := snapshotTree(t, dbDir); after != before {
		t.Fatalf("probe changed the running server's database files:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}
