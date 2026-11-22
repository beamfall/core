// Package storetest opens real, migrated, seeded SQLite stores for tests.
package storetest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	_ "modernc.org/sqlite"
)

// Open returns a development-seeded store on a fresh temp dir, closed at test end.
func Open(t testing.TB) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), MigratedDataDir(t), store.Options{SeedDevelopmentData: true})
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
	st, err := store.Open(context.Background(), MigratedDataDir(t), store.Options{SeedProfile: store.SeedProfileConformance})
	if err != nil {
		t.Fatalf("storetest.OpenConformance: %v", err)
	}
	InstallSnapshotSealer(st)
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// Replaying the full catalog + events migration chain costs seconds per store
// under -race. The template below is migrated once per test binary, lazily,
// and kept in memory; MigratedDataDir hands each caller a fresh temp data dir
// holding private byte copies of both databases, so store.Open finds the
// schema current and only seeds. The template is unseeded because several
// seed profiles embed the data dir's own paths in their rows.
var (
	templateOnce    sync.Once
	templateCatalog []byte
	templateEvents  []byte
	templateErr     error
)

// MigratedDataDir returns a fresh temp data dir (removed at test end) whose
// db/catalog.db and db/events.db are private copies of a database migrated to
// the current schema. Pass it to store.Open in place of t.TempDir() when the
// test does not assert migration behaviour itself.
func MigratedDataDir(t testing.TB) string {
	t.Helper()
	templateOnce.Do(func() { templateCatalog, templateEvents, templateErr = buildTemplate() })
	if templateErr != nil {
		t.Fatalf("storetest migrated template: %v", templateErr)
	}
	dataDir := t.TempDir()
	dbDir := filepath.Join(dataDir, "db")
	if err := os.MkdirAll(dbDir, 0o700); err != nil {
		t.Fatalf("storetest.MigratedDataDir: %v", err)
	}
	for name, raw := range map[string][]byte{"catalog.db": templateCatalog, "events.db": templateEvents} {
		if err := os.WriteFile(filepath.Join(dbDir, name), raw, 0o600); err != nil {
			t.Fatalf("storetest.MigratedDataDir: %v", err)
		}
	}
	return dataDir
}

func buildTemplate() (catalog, events []byte, err error) {
	dir, err := os.MkdirTemp("", "beamfall-storetest-template-")
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	st, err := store.Open(context.Background(), dir, store.Options{Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		return nil, nil, err
	}
	if err := st.Close(); err != nil {
		return nil, nil, err
	}
	dbDir := filepath.Join(dir, "db")
	// Closing the last connection checkpoints the WAL; a leftover sidecar would
	// mean the main files alone are not the whole database.
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

// RecordedSourceEnd reads the persisted EOF for a source generation in the
// local fixture catalog.
func RecordedSourceEnd(t testing.TB, dataDir, fingerprint, generation string) float64 {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "db", "catalog.db"))
	if err != nil {
		t.Fatalf("open catalog fixture: %v", err)
	}
	defer db.Close()
	var end float64
	err = db.QueryRowContext(t.Context(), `SELECT end_seconds FROM source_end_observation WHERE content_fingerprint = ? AND generation = ?`, fingerprint, generation).Scan(&end)
	if err != nil {
		t.Fatalf("read recorded source end: %v", err)
	}
	return end
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

// RemoveMediaPart removes a Part from the local fixture catalog to model a
// Version changing while a playback probe is running.
func RemoveMediaPart(t testing.TB, dataDir string, file store.MediaFile) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "db", "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	result, err := db.ExecContext(t.Context(), `DELETE FROM media_file WHERE media_id = ? AND content_fingerprint = ? AND part_ordinal > 0`, file.MediaID, file.ContentFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		t.Fatalf("removed %d fixture Parts: %v", rows, err)
	}
}

// BackdateStreamTokenActivity moves every stream-token ledger row of one
// session back to when, as if its player had gone quiet then (CORE-2305). It
// lives under internal/store so storage-driver access stays inside the store
// boundary.
func BackdateStreamTokenActivity(t testing.TB, dataDir string, session store.SessionID, when time.Time) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "db", "events.db"))
	if err != nil {
		t.Fatalf("open events fixture: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE stream_token_ledger SET last_active_ms = ? WHERE session_id = ?`, when.UnixMilli(), session); err != nil {
		t.Fatalf("backdate stream token activity: %v", err)
	}
}

// RejectIntegrationIPSettlement injects a write failure on the second lane
// of an integration settlement while still allowing reservations.
func RejectIntegrationIPSettlement(t testing.TB, dataDir string) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "db", "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TRIGGER fixture_reject_ip_settlement BEFORE UPDATE OF success ON auth_attempt
		WHEN OLD.action = 'integration.login' AND OLD.key LIKE 'ip:%' AND NEW.success = 1
		BEGIN SELECT RAISE(ABORT, 'fixture settlement failure'); END`); err != nil {
		t.Fatal(err)
	}
}

// SeedAudioMedia creates catalog rows for tests that need a large visible set
// without requiring playable files. It returns the IDs in fixture order.
func SeedAudioMedia(t testing.TB, dataDir string, libraryID store.LibraryID, count int) []store.MediaID {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "db", "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	result, err := db.ExecContext(t.Context(), `
		WITH RECURSIVE fixture(n) AS (
			SELECT 0 WHERE ?1 > 0
			UNION ALL SELECT n + 1 FROM fixture WHERE n + 1 < ?1
		)
		INSERT INTO media(id, library_id, title, duration_seconds, container, codec, media_class)
		SELECT printf('fixture-audio-%05d', n), ?2, printf('Fixture audio %05d', n), 60, 'flac', 'flac', 'audio'
		FROM fixture
	`, count, libraryID)
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := result.RowsAffected(); err != nil || rows != int64(count) {
		t.Fatalf("seeded %d audio rows, want %d: %v", rows, count, err)
	}
	ids := make([]store.MediaID, count)
	for i := range ids {
		ids[i] = store.MediaID(fmt.Sprintf("fixture-audio-%05d", i))
	}
	return ids
}

// BreakEngagementRead verifies the seeded engagement projection works, then
// makes its playback-progress read fail without changing the queue fixture.
func BreakEngagementRead(t testing.TB, st *store.Store, dataDir string, profileID store.ProfileID) {
	t.Helper()
	items := []store.SourceItem{{ID: "media-alpha", Kind: "media"}}
	if err := st.AnnotateEngagement(t.Context(), profileID, store.LibraryFilter{}, items, nil); err != nil {
		t.Fatalf("verify engagement fixture: %v", err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "db", "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(t.Context(), "ALTER TABLE playback_progress RENAME TO unavailable_playback_progress"); err != nil {
		t.Fatal(err)
	}
	if err := st.AnnotateEngagement(t.Context(), profileID, store.LibraryFilter{}, items, nil); err == nil || !strings.Contains(err.Error(), "no such table: playback_progress") {
		t.Fatalf("fault did not reach the engagement read: %v", err)
	}
}

// CorruptProfileEntryPIN replaces an existing fixture verifier with malformed data.
func CorruptProfileEntryPIN(t testing.TB, dataDir string, id store.ProfileID) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "db", "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	result, err := db.Exec(`UPDATE server_config SET value = 'invalid' WHERE key = ?`, "profile.entry_pin:"+string(id))
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		t.Fatalf("malformed verifier fixture updated %d rows: %v", rows, err)
	}
}

// SetLibraryPlugin assigns a plugin to a seeded fixture Library, which the dev
// seed leaves plugin-less. Library health judges a Library by its plugin
// (CORE-2383). It lives under internal/store so storage-driver access stays
// inside the store boundary.
func SetLibraryPlugin(t testing.TB, dataDir string, id store.LibraryID, pluginID string) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "db", "catalog.db"))
	if err != nil {
		t.Fatalf("open catalog fixture: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE library SET plugin_id = ? WHERE id = ?`, pluginID, id); err != nil {
		t.Fatalf("set library plugin: %v", err)
	}
}

// SeedFilelessGUIDMedia inserts a catalogued audio Media with no media_file,
// keyed by the provisional GUID External ID, the shape a feed catalogue gives an
// entry before it is played or downloaded. The ID is
// store.ProvisionalMediaID(libraryID, guid). It lives under internal/store so
// storage-driver access stays inside the store boundary.
func SeedFilelessGUIDMedia(t testing.TB, dataDir string, libraryID store.LibraryID, guid, title string) store.MediaID {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "db", "catalog.db"))
	if err != nil {
		t.Fatalf("open catalog fixture: %v", err)
	}
	defer db.Close()
	mediaID := store.ProvisionalMediaID(libraryID, guid)
	if _, err := db.Exec(`
		INSERT INTO media(id, library_id, title, duration_seconds, container, codec, media_class, mutated_by)
		VALUES (?, ?, ?, 1800, 'mp3', 'mp3', 'audio', 'scan')
	`, mediaID, libraryID, title); err != nil {
		t.Fatalf("seed file-less media: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO media_external_id(media_id, scheme, value) VALUES (?, ?, ?)`, mediaID, store.ProvisionalExternalIDScheme, guid); err != nil {
		t.Fatalf("seed file-less media external ID: %v", err)
	}
	return mediaID
}
