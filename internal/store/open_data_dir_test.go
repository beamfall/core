package store

import (
	"bytes"
	"context"
	"errors"
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
