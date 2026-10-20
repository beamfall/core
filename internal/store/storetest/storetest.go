// Package storetest opens real, migrated, seeded SQLite stores for tests.
package storetest

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	_ "modernc.org/sqlite"
)

// Open returns a development-seeded store on a fresh temp dir, closed at test end.
func Open(t testing.TB) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir(), store.Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatalf("storetest.Open: %v", err)
	}
	InstallSnapshotSealer(st)
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// OpenAt returns a development-seeded store on the CALLER'S data dir, closed at
// test end. Open picks a fresh temp dir every time, which cannot express a
// restart; OpenAt lets a test close one store and open another over the same
// bytes, which is the only honest way to prove that a fact outlives the process
// holding it.
func OpenAt(t testing.TB, dataDir string) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), dataDir, store.Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatalf("storetest.OpenAt: %v", err)
	}
	InstallSnapshotSealer(st)
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// OpenConformance returns a conformance-seeded store on a fresh temp dir,
// closed at test end. It mirrors Open but selects the deterministic conformance
// corpus via store.Options.SeedProfile = store.SeedProfileConformance (which
// subsumes the development seed and adds the corpus Media Files).
//
// This seeds only the conformance STORE. It deliberately does NOT replicate the
// app-layer read-clock substitution (internal/app/app.go pins a fixed read clock
// + fixed Stream-Token key for the conformance world); those belong to the
// wire-contract services, not to store seeding, so the integrator wires them
// separately where the byte-identical HTTP goldens are needed.
func OpenConformance(t testing.TB) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir(), store.Options{SeedProfile: store.SeedProfileConformance})
	if err != nil {
		t.Fatalf("storetest.OpenConformance: %v", err)
	}
	InstallSnapshotSealer(st)
	t.Cleanup(func() { _ = st.Close() })
	return st
}

type snapshotSealer struct{}

// InstallSnapshotSealer supplies the lightweight envelope double used by
// store consumers whose tests exercise orchestration rather than cryptography.
func InstallSnapshotSealer(st *store.Store) {
	st.SetSnapshotSealer(snapshotSealer{})
}

func (snapshotSealer) SealDirectory(ctx context.Context, _, dest string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "storetest", os.WriteFile(dest, []byte("storetest encrypted envelope"), 0o600)
}

func (snapshotSealer) VerifyEnvelopeStructure(src io.Reader) error {
	raw, err := io.ReadAll(src)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return errors.New("empty storetest envelope")
	}
	return nil
}

// OpenSQLiteDB returns a raw SQLite database on a fresh temp file, closed at
// test end.
func OpenSQLiteDB(t testing.TB) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", t.TempDir()+"/fixture.db")
	if err != nil {
		t.Fatalf("storetest.OpenSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// BackdateTrashedMedia updates a fixture trash timestamp. It lives under
// internal/store so storage-driver access stays inside the store boundary.
func BackdateTrashedMedia(t testing.TB, dataDir string, id store.MediaID, when time.Time) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "db", "catalog.db"))
	if err != nil {
		t.Fatalf("open catalog fixture: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE media SET trashed_at_ms = ? WHERE id = ?`, when.UnixMilli(), id); err != nil {
		t.Fatalf("backdate trashed media: %v", err)
	}
}
