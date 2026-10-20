package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestOpenSeedsDevelopmentData(t *testing.T) {
	store, err := Open(context.Background(), t.TempDir(), Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()

	profile, err := store.Profile(context.Background(), "profile-default")
	if err != nil {
		t.Fatalf("Profile returned error: %v", err)
	}
	if profile.Kind != ProfileAdult {
		t.Fatalf("Kind = %q, want adult", profile.Kind)
	}
	if _, err := store.VerifyDeviceSecret(context.Background(), "device-mac", "dev-manager-secret"); err != nil {
		t.Fatalf("VerifyDeviceSecret returned error: %v", err)
	}
}

func TestOpenSeedsDemoProfile(t *testing.T) {
	dataDir := t.TempDir()
	st, err := Open(context.Background(), dataDir, Options{SeedProfile: SeedProfileDemo})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer st.Close()

	hidden := LibraryFilter{AllowedIDs: []LibraryID{"library-demo-open", "library-demo-archive", "library-demo-guarded"}}
	revealed := hidden
	revealed.Revealed = true

	hiddenLibraries, err := st.Libraries(context.Background(), hidden)
	if err != nil {
		t.Fatalf("Libraries hidden returned error: %v", err)
	}
	if got, want := len(hiddenLibraries), 2; got != want {
		t.Fatalf("hidden Libraries = %d, want %d: %#v", got, want, hiddenLibraries)
	}
	revealedLibraries, err := st.Libraries(context.Background(), revealed)
	if err != nil {
		t.Fatalf("Libraries revealed returned error: %v", err)
	}
	if got, want := len(revealedLibraries), 3; got != want {
		t.Fatalf("revealed Libraries = %d, want %d: %#v", got, want, revealedLibraries)
	}

	openItems := mustSourceItems(t, st, hidden, SourceQuery{Kind: "all-items", LibraryID: "library-demo-open", Limit: 20})
	if got, wantAtLeast := len(openItems), 8; got < wantAtLeast {
		t.Fatalf("open demo items = %d, want at least %d: %#v", got, wantAtLeast, openItems)
	}
	if _, err := st.Media(context.Background(), "media-demo-guarded-1", hidden); err == nil {
		t.Fatal("hidden filter read restricted demo media")
	}
	if _, err := st.Media(context.Background(), "media-demo-guarded-1", revealed); err != nil {
		t.Fatalf("revealed filter did not read restricted demo media: %v", err)
	}

	categories, err := st.EntitiesByKind(context.Background(), "library-demo-open", "category", hidden, EntityKindOptions{}, 20, 0)
	if err != nil {
		t.Fatalf("EntitiesByKind category returned error: %v", err)
	}
	if len(categories) < 2 {
		t.Fatalf("categories = %#v, want at least two", categories)
	}
	comedyItems := mustSourceItems(t, st, hidden, SourceQuery{
		Kind:         "all-items",
		LibraryID:    "library-demo-open",
		EntityFilter: &EntityFilter{Kind: "category", Name: "Comedy"},
		Limit:        20,
	})
	if len(comedyItems) < 2 {
		t.Fatalf("Comedy shelf items = %#v, want at least two", comedyItems)
	}

	continueItems := mustSourceItems(t, st, hidden, SourceQuery{Kind: "continue-watching", ProfileID: "profile-demo-default"})
	if got, want := len(continueItems), 5; got < want {
		t.Fatalf("continue-watching = %#v, want at least %d in-progress items", continueItems, want)
	}
	if continueItems[0].ID != "media-demo-personal-07" {
		t.Fatalf("continue-watching first = %#v, want media-demo-personal-07", continueItems)
	}
	for _, item := range continueItems {
		if strings.HasPrefix(string(item.ID), "media-demo-restricted-") {
			t.Fatalf("hidden continue-watching leaked restricted item: %#v", continueItems)
		}
	}
	revealedContinueItems := mustSourceItems(t, st, revealed, SourceQuery{Kind: "continue-watching", ProfileID: "profile-demo-default"})
	var foundRestricted bool
	for _, item := range revealedContinueItems {
		if item.ID == "media-demo-restricted-05" {
			foundRestricted = true
			break
		}
	}
	if !foundRestricted {
		t.Fatalf("revealed continue-watching = %#v, want media-demo-restricted-05", revealedContinueItems)
	}
	nextItems := mustSourceItems(t, st, hidden, SourceQuery{Kind: "next-up", ProfileID: "profile-demo-default"})
	if len(nextItems) == 0 || nextItems[0].ID != "media-demo-series-2" {
		t.Fatalf("next-up = %#v, want media-demo-series-2 first", nextItems)
	}

	markers, err := st.MarkersForMedia(context.Background(), "media-demo-sintel", hidden)
	if err != nil {
		t.Fatalf("MarkersForMedia returned error: %v", err)
	}
	if len(markers) < 2 {
		t.Fatalf("markers = %#v, want at least two", markers)
	}
	file, err := st.PrimaryMediaFile(context.Background(), "media-demo-sintel", hidden)
	if err != nil {
		t.Fatalf("PrimaryMediaFile returned error: %v", err)
	}
	if _, err := os.Stat(file.Path); err != nil {
		t.Fatalf("seed placeholder missing at %s: %v", file.Path, err)
	}
	assertSeedArtworkDimensions(t, dataDir, "media-demo-personal-07", "poster", 600, 900)
	assertSeedArtworkDimensions(t, dataDir, "media-demo-personal-07", "backdrop", 1280, 720)
	assertSeedArtworkDimensions(t, dataDir, "media-demo-restricted-05", "poster", 600, 900)
	assertSeedArtworkDimensions(t, dataDir, "media-demo-restricted-05", "backdrop", 1280, 720)
}

func assertSeedArtworkDimensions(t *testing.T, dataDir string, mediaID MediaID, slot string, wantWidth int, wantHeight int) {
	t.Helper()
	path := filepath.Join(dataDir, "artwork", string(mediaID), slot+".jpg")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open seeded artwork %s: %v", path, err)
	}
	defer f.Close()
	cfg, format, err := image.DecodeConfig(f)
	if err != nil {
		t.Fatalf("decode seeded artwork %s: %v", path, err)
	}
	if format != "jpeg" {
		t.Fatalf("seeded artwork %s format = %q, want jpeg", path, format)
	}
	if cfg.Width != wantWidth || cfg.Height != wantHeight {
		t.Fatalf("seeded artwork %s = %dx%d, want %dx%d", path, cfg.Width, cfg.Height, wantWidth, wantHeight)
	}
}

func TestOpenPersistsEnrollmentAcrossReopen(t *testing.T) {
	dataDir := t.TempDir()
	first, err := Open(context.Background(), dataDir, Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	expiresAt := time.Unix(200, 0).UTC()
	if err := first.EnrollDevice(
		context.Background(),
		Device{ID: "device-new", Name: "Living Room", Role: DeviceRoleViewer, SecretHash: HashDeviceSecret("device-secret")},
		Session{ID: "session-new", DeviceID: "device-new", ProfileID: "profile-default", ExpiresAt: expiresAt},
	); err != nil {
		t.Fatalf("EnrollDevice returned error: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	second, err := Open(context.Background(), dataDir, Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatalf("reopen returned error: %v", err)
	}
	defer second.Close()
	session, err := second.Session(context.Background(), "session-new")
	if err != nil {
		t.Fatalf("Session returned error after reopen: %v", err)
	}
	if !session.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("ExpiresAt = %s, want %s", session.ExpiresAt, expiresAt)
	}
	if _, err := second.VerifyDeviceSecret(context.Background(), "device-new", "device-secret"); err != nil {
		t.Fatalf("VerifyDeviceSecret returned error after reopen: %v", err)
	}
}

func TestOpenPersistsRevocationAcrossReopen(t *testing.T) {
	dataDir := t.TempDir()
	first, err := Open(context.Background(), dataDir, Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if err := first.RevokeDevice(context.Background(), "device-tv"); err != nil {
		t.Fatalf("RevokeDevice returned error: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	second, err := Open(context.Background(), dataDir, Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatalf("reopen returned error: %v", err)
	}
	defer second.Close()
	device, err := second.Device(context.Background(), "device-tv")
	if err != nil {
		t.Fatalf("Device returned error: %v", err)
	}
	if !device.Revoked {
		t.Fatal("device revocation did not persist")
	}
}

func TestOpenPersistsPairingCodeAcrossReopen(t *testing.T) {
	dataDir := t.TempDir()
	first, err := Open(context.Background(), dataDir, Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	expiresAt := time.Unix(600, 0).UTC()
	if err := first.CreatePairingCode(context.Background(), PairingCode{
		Code:       "PAIR1234",
		ProfileID:  "profile-default",
		DeviceRole: DeviceRoleViewer,
		ExpiresAt:  expiresAt,
	}); err != nil {
		t.Fatalf("CreatePairingCode returned error: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := Open(context.Background(), dataDir, Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatalf("reopen returned error: %v", err)
	}
	defer second.Close()
	code, err := second.ConsumePairingCode(context.Background(), "PAIR1234", time.Unix(100, 0).UTC())
	if err != nil {
		t.Fatalf("ConsumePairingCode returned error: %v", err)
	}
	if code.ProfileID != "profile-default" {
		t.Fatalf("ProfileID = %q, want profile-default", code.ProfileID)
	}
}

func TestOpenPersistsPairingCodeConsumptionAcrossReopen(t *testing.T) {
	dataDir := t.TempDir()
	first, err := Open(context.Background(), dataDir, Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.CreatePairingCode(context.Background(), PairingCode{
		Code:       "PAIR1234",
		ProfileID:  "profile-default",
		DeviceRole: DeviceRoleViewer,
		ExpiresAt:  time.Unix(600, 0).UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := first.ConsumePairingCode(context.Background(), "PAIR1234", time.Unix(100, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := Open(context.Background(), dataDir, Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err := second.ConsumePairingCode(context.Background(), "PAIR1234", time.Unix(101, 0).UTC()); err == nil {
		t.Fatal("reopened store accepted consumed pairing code")
	}
}

func TestOpenPersistsMediaFileAcrossReopen(t *testing.T) {
	dataDir := t.TempDir()
	mediaPath := filepath.Join(t.TempDir(), "media.bin")
	if err := os.WriteFile(mediaPath, []byte("beamfall bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := Open(context.Background(), dataDir, Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.SetPrimaryMediaFile(context.Background(), "media-alpha", mediaPath); err != nil {
		t.Fatalf("SetPrimaryMediaFile returned error: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := Open(context.Background(), dataDir, Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	file, err := second.PrimaryMediaFile(context.Background(), "media-alpha", LibraryFilter{AllowedIDs: []LibraryID{"library-local"}})
	if err != nil {
		t.Fatalf("PrimaryMediaFile returned error: %v", err)
	}
	if file.Path != mediaPath {
		t.Fatalf("Path = %q, want %q", file.Path, mediaPath)
	}
}

func TestOpenRefusesNewerCatalogSchema(t *testing.T) {
	dataDir := t.TempDir()
	store, err := Open(context.Background(), dataDir, Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.catalogW.Exec("PRAGMA user_version = 999"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = Open(context.Background(), dataDir, Options{SeedDevelopmentData: true})

	if err == nil {
		t.Fatal("Open accepted newer catalog schema")
	}
}

func TestOpenRefusesNewerEventsSchema(t *testing.T) {
	dataDir := t.TempDir()
	store, err := Open(context.Background(), dataDir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.eventsW.Exec("PRAGMA user_version = 999"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = Open(context.Background(), dataDir, Options{})

	if err == nil {
		t.Fatal("Open accepted newer events schema")
	}
}

func TestOpenCreatesJobTablesAndEventsVersion(t *testing.T) {
	store, err := Open(context.Background(), t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	version, err := userVersion(context.Background(), store.eventsW)
	if err != nil {
		t.Fatal(err)
	}
	if version != 21 {
		t.Fatalf("events user_version = %d, want 21", version)
	}
	for _, name := range []string{"job", "job_checkpoint", "job_event", "event_outbox", "schedule_occurrence", "schedule_occurrence_member"} {
		if !tableExists(t, store.eventsW, name) {
			t.Fatalf("%s table was not created", name)
		}
	}
}

func TestOpenCreatesTwoDatabaseFiles(t *testing.T) {
	dataDir := t.TempDir()
	store, err := Open(context.Background(), dataDir, Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	for _, name := range []string{"catalog.db", "events.db"} {
		if exists := fileExists(filepath.Join(dataDir, "db", name)); !exists {
			t.Fatalf("%s was not created", name)
		}
	}
}

func TestOpenRefusesNetworkDataDir(t *testing.T) {
	restore := replaceFilesystemDetector(func(string) (dataDirFilesystem, error) {
		return classifyDataDirFilesystem("smbfs", 0), nil
	})
	defer restore()

	_, err := Open(context.Background(), t.TempDir(), Options{})

	if err == nil {
		t.Fatal("Open accepted network data dir")
	}
	if !strings.Contains(err.Error(), "--allow-network-data-dir") {
		t.Fatalf("error %q does not name override flag", err)
	}
}

func TestOpenAllowsNetworkDataDirWithExplicitOverride(t *testing.T) {
	restore := replaceFilesystemDetector(func(string) (dataDirFilesystem, error) {
		return classifyDataDirFilesystem("smbfs", 0), nil
	})
	defer restore()

	store, err := Open(context.Background(), t.TempDir(), Options{AllowNetworkDataDir: true})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()
}

func TestMigrateCreatesPreMigrationBackup(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "catalog.db")
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	backupDir := filepath.Join(t.TempDir(), "backups")
	migrations := fstest.MapFS{
		"migrations/0001_initial.sql": {Data: []byte("CREATE TABLE item (id TEXT PRIMARY KEY);")},
	}

	if err := migrate(ctx, slog.Default(), db, dbPath, "catalog", migrations, "migrations", backupDir); err != nil {
		t.Fatalf("migrate returned error: %v", err)
	}

	dbBackups, err := filepath.Glob(filepath.Join(backupDir, "*-catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	if len(dbBackups) != 1 {
		t.Fatalf("catalog backups = %v, want one db backup", dbBackups)
	}
	info, err := os.Stat(dbBackups[0])
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode = %v, want 0600", info.Mode().Perm())
	}
	backupDB, err := openSQLite(dbBackups[0])
	if err != nil {
		t.Fatal(err)
	}
	defer backupDB.Close()
	if tableExists(t, backupDB, "item") {
		t.Fatal("backup contains post-migration table")
	}
	if !tableExists(t, db, "item") {
		t.Fatal("live database is missing migrated table")
	}
	manifests, err := filepath.Glob(filepath.Join(backupDir, "*-catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(manifests) != 1 {
		t.Fatalf("catalog manifests = %v, want one manifest", manifests)
	}
}

func TestMigrateRejectsHistoricalMigrationChecksumDrift(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "catalog.db")
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	backupDir := filepath.Join(t.TempDir(), "backups")
	original := fstest.MapFS{
		"migrations/0001_initial.sql": {Data: []byte("CREATE TABLE item (id TEXT PRIMARY KEY);")},
	}

	if err := migrate(ctx, slog.Default(), db, dbPath, "catalog", original, "migrations", backupDir); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if err := migrate(ctx, slog.Default(), db, dbPath, "catalog", original, "migrations", backupDir); err != nil {
		t.Fatalf("unchanged migrate: %v", err)
	}

	mutated := fstest.MapFS{
		"migrations/0001_initial.sql": {Data: []byte("CREATE TABLE item (id TEXT PRIMARY KEY, title TEXT);")},
	}
	err = migrate(ctx, slog.Default(), db, dbPath, "catalog", mutated, "migrations", backupDir)
	if err == nil || !strings.Contains(err.Error(), "catalog migration 0001_initial.sql checksum drift") {
		t.Fatalf("mutated historical migration error = %v, want checksum drift rejection", err)
	}
}

// TestMigratePreMigrationBackupRestoresPriorSchema exercises the full
// snapshot-restore round trip the pre-migration backup exists to support: apply
// a first migration, add a second one and re-migrate, then confirm the snapshot
// taken before the second migration is a valid database carrying the prior
// schema and data — not the new schema. Restoring it (here: re-opening the
// snapshot file) yields exactly the pre-migration state.
func TestMigratePreMigrationBackupRestoresPriorSchema(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "catalog.db")
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	backupDir := filepath.Join(t.TempDir(), "backups")

	v1 := fstest.MapFS{
		"migrations/0001_initial.sql": {Data: []byte("CREATE TABLE item (id TEXT PRIMARY KEY);")},
	}
	if err := migrate(ctx, slog.Default(), db, dbPath, "catalog", v1, "migrations", backupDir); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	// Seed a row so the snapshot must preserve data, not just schema.
	if _, err := db.ExecContext(ctx, "INSERT INTO item(id) VALUES ('alpha')"); err != nil {
		t.Fatal(err)
	}

	// The initial 0->1 migration snapshots the (empty) starting database; drop
	// those so the only remaining backups are the ones taken before v2.
	preV2, err := filepath.Glob(filepath.Join(backupDir, "*-catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range preV2 {
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}

	v2 := fstest.MapFS{
		"migrations/0001_initial.sql":  {Data: []byte("CREATE TABLE item (id TEXT PRIMARY KEY);")},
		"migrations/0002_addition.sql": {Data: []byte("CREATE TABLE extra (id TEXT PRIMARY KEY);")},
	}
	if err := migrate(ctx, slog.Default(), db, dbPath, "catalog", v2, "migrations", backupDir); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	// Live DB has the post-migration schema.
	if !tableExists(t, db, "extra") {
		t.Fatal("live database missing the second migration's table")
	}

	// The snapshot taken before v2 is restorable: open it and confirm it holds
	// the pre-v2 schema (item, no extra) with the seeded row intact.
	snaps, err := filepath.Glob(filepath.Join(backupDir, "*-catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 1 {
		t.Fatalf("pre-v2 catalog snapshots = %v, want exactly one", snaps)
	}
	restored, err := openSQLite(snaps[0])
	if err != nil {
		t.Fatalf("open pre-migration snapshot: %v", err)
	}
	defer restored.Close()
	if !tableExists(t, restored, "item") {
		t.Fatal("snapshot missing the pre-migration table")
	}
	if tableExists(t, restored, "extra") {
		t.Fatal("snapshot already contains the post-migration table")
	}
	var id string
	if err := restored.QueryRowContext(ctx, "SELECT id FROM item").Scan(&id); err != nil {
		t.Fatalf("read seeded row from snapshot: %v", err)
	}
	if id != "alpha" {
		t.Fatalf("snapshot row id = %q, want alpha", id)
	}
	var ver int
	if err := restored.QueryRowContext(ctx, "PRAGMA user_version").Scan(&ver); err != nil {
		t.Fatal(err)
	}
	if ver != 1 {
		t.Fatalf("snapshot user_version = %d, want 1 (pre-second-migration)", ver)
	}
}

func TestMigrateRepairsLegacyMediaFileStreamHDR10Plus(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "catalog.db")
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE schema_migrations (
		  name TEXT PRIMARY KEY,
		  checksum TEXT NOT NULL,
		  applied_at INTEGER NOT NULL
		);
		CREATE TABLE media_file_stream (
		  content_fingerprint TEXT NOT NULL,
		  stream_index INTEGER NOT NULL,
		  color_transfer TEXT NOT NULL DEFAULT '',
		  color_primaries TEXT NOT NULL DEFAULT '',
		  PRIMARY KEY (content_fingerprint, stream_index)
		);
		INSERT INTO schema_migrations(name, checksum, applied_at)
		VALUES ('0050_media_file_streams.sql', '27ad18c20eea2a55087f59ff3e5c37bf82d4785b4eb7247797b344f67c3f36e6', unixepoch());
		PRAGMA user_version = 1;
	`); err != nil {
		t.Fatal(err)
	}
	migrations := fstest.MapFS{
		"migrations/0050_media_file_streams.sql": {
			Data: []byte("CREATE TABLE media_file_stream(content_fingerprint TEXT PRIMARY KEY, hdr10_plus INTEGER NOT NULL DEFAULT 0);"),
		},
		"migrations/0073_media_file_stream_hdr10_plus.sql": {
			Data: []byte("SELECT 1;"),
		},
	}

	if err := migrate(ctx, slog.Default(), db, dbPath, "catalog", migrations, "migrations", filepath.Join(t.TempDir(), "backups")); err != nil {
		t.Fatalf("migrate returned error: %v", err)
	}
	if !columnExists(t, db, "media_file_stream", "hdr10_plus") {
		t.Fatal("media_file_stream.hdr10_plus missing after compatibility repair")
	}
}

func TestMigrateRepairsLegacyMediaFileStreamLevel(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "catalog.db")
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE schema_migrations (
		  name TEXT PRIMARY KEY,
		  checksum TEXT NOT NULL,
		  applied_at INTEGER NOT NULL
		);
		CREATE TABLE media_file_stream (
		  content_fingerprint TEXT NOT NULL,
		  stream_index INTEGER NOT NULL,
		  profile TEXT NOT NULL DEFAULT '',
		  hdr10_plus INTEGER NOT NULL DEFAULT 0,
		  PRIMARY KEY (content_fingerprint, stream_index)
		);
		INSERT INTO schema_migrations(name, checksum, applied_at)
		VALUES ('0050_media_file_streams.sql', 'bec2759c8867219760cb9e3c875e9f4d10621613fd153f105e8ff0d5ebc32412', unixepoch());
		PRAGMA user_version = 1;
	`); err != nil {
		t.Fatal(err)
	}
	migrations := fstest.MapFS{
		"migrations/0050_media_file_streams.sql": {
			Data: []byte("CREATE TABLE media_file_stream(content_fingerprint TEXT PRIMARY KEY, level INTEGER NOT NULL DEFAULT 0);"),
		},
		"migrations/0074_media_file_stream_level.sql": {
			Data: []byte("SELECT 1;"),
		},
	}

	if err := migrate(ctx, slog.Default(), db, dbPath, "catalog", migrations, "migrations", filepath.Join(t.TempDir(), "backups")); err != nil {
		t.Fatalf("migrate returned error: %v", err)
	}
	if !columnExists(t, db, "media_file_stream", "level") {
		t.Fatal("media_file_stream.level missing after compatibility repair")
	}
}

func TestMigrateEncryptedLibraryAddsMediaFileStreamDispositions(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "encrypted_library.db")
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	migration1 := []byte("SELECT 1;")
	migration2 := []byte(`
		CREATE TABLE media_file_stream (
		  content_fingerprint TEXT NOT NULL,
		  stream_index INTEGER NOT NULL,
		  PRIMARY KEY (content_fingerprint, stream_index)
		);
	`)
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE schema_migrations (
		  name TEXT PRIMARY KEY,
		  checksum TEXT NOT NULL,
		  applied_at INTEGER NOT NULL
		);
		CREATE TABLE media_file_stream (
		  content_fingerprint TEXT NOT NULL,
		  stream_index INTEGER NOT NULL,
		  PRIMARY KEY (content_fingerprint, stream_index)
		);
		PRAGMA user_version = 2;
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx,
		"INSERT INTO schema_migrations(name, checksum, applied_at) VALUES (?, ?, unixepoch()), (?, ?, unixepoch())",
		"0001_initial.sql", migrationChecksum(migration1),
		"0002_routed_private_catalog.sql", migrationChecksum(migration2),
	); err != nil {
		t.Fatal(err)
	}

	migrations := fstest.MapFS{
		"migrations/0001_initial.sql": {
			Data: migration1,
		},
		"migrations/0002_routed_private_catalog.sql": {
			Data: migration2,
		},
		"migrations/0003_media_file_stream_dispositions.sql": {
			Data: []byte(`
				ALTER TABLE media_file_stream ADD COLUMN is_hearing_impaired INTEGER NOT NULL DEFAULT 0;
				ALTER TABLE media_file_stream ADD COLUMN is_visual_impaired INTEGER NOT NULL DEFAULT 0;
				ALTER TABLE media_file_stream ADD COLUMN is_commentary INTEGER NOT NULL DEFAULT 0;
			`),
		},
	}

	if err := migrate(ctx, slog.Default(), db, dbPath, "encrypted_library", migrations, "migrations", filepath.Join(t.TempDir(), "backups")); err != nil {
		t.Fatalf("migrate returned error: %v", err)
	}
	for _, column := range []string{"is_hearing_impaired", "is_visual_impaired", "is_commentary"} {
		if !columnExists(t, db, "media_file_stream", column) {
			t.Fatalf("media_file_stream.%s missing after encrypted-library migration", column)
		}
	}
}

// TestOpenRoutesMigrationLogsToInjectedLogger asserts the migration runner logs
// through Options.Logger rather than the global default: an injected buffer
// captures the "applying migrations" line, and a quiet (io.Discard) logger
// silences it — the regression the conformance test hit.
func TestOpenRoutesMigrationLogsToInjectedLogger(t *testing.T) {
	var buf bytes.Buffer
	injected := slog.New(slog.NewTextHandler(&buf, nil))
	st, err := Open(context.Background(), t.TempDir(), Options{Logger: injected})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer st.Close()
	if !strings.Contains(buf.String(), "applying migrations") {
		t.Fatalf("injected logger did not receive migration logs; got %q", buf.String())
	}

	// A quiet logger (level above Info, capturing to its own buffer) silences
	// the Info-level migration lines a caller wants suppressed.
	var quietBuf bytes.Buffer
	quiet := slog.New(slog.NewTextHandler(&quietBuf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	st2, err := Open(context.Background(), t.TempDir(), Options{Logger: quiet})
	if err != nil {
		t.Fatalf("Open with quiet logger returned error: %v", err)
	}
	defer st2.Close()
	if quietBuf.Len() != 0 {
		t.Fatalf("quiet logger leaked migration output: %q", quietBuf.String())
	}
}

func TestMigrateRollsBackAllPendingMigrationsOnFailure(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "catalog.db")
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	migrations := fstest.MapFS{
		"migrations/0001_ok.sql": {
			Data: []byte("CREATE TABLE committed (id TEXT PRIMARY KEY);"),
		},
		"migrations/0002_bad.sql": {
			Data: []byte(`
				CREATE TABLE partial (id TEXT PRIMARY KEY);
				INSERT INTO missing_table(id) VALUES ('x');
			`),
		},
	}

	if err := migrate(ctx, slog.Default(), db, dbPath, "catalog", migrations, "migrations", filepath.Join(t.TempDir(), "backups")); err == nil {
		t.Fatal("migrate accepted failing migration")
	}
	version, err := userVersion(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if version != 0 {
		t.Fatalf("user_version = %d, want 0", version)
	}
	if tableExists(t, db, "committed") {
		t.Fatal("failed migration run left earlier pending migration behind")
	}
	if tableExists(t, db, "partial") {
		t.Fatal("failed migration left partial table behind")
	}
	if tableExists(t, db, "schema_migrations") {
		var count int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("schema_migrations rows = %d, want 0", count)
		}
	}
}

// TestCheckForeignKeysDetectsOrphan proves the migration guard fires on exactly
// the corruption shape the M8 footgun produces: a child row referencing a
// missing parent that SQLite's immediate enforcement never saw because it was
// created with foreign_keys OFF — the state the safe table-rebuild procedure
// (CREATE _new → INSERT…SELECT → DROP → RENAME) runs in. checkForeignKeys must
// return an error naming the migration, the offending child table, and the
// referenced parent. A clean graph returns nil.
func TestCheckForeignKeysDetectsOrphan(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "catalog.db")
	// foreign_keys OFF mirrors the rebuild path: the orphan lands silently and
	// only PRAGMA foreign_key_check (the guard) can surface it.
	dsn := (&url.URL{Scheme: "file", Path: path}).String() + "?_pragma=foreign_keys(0)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE parent (id TEXT PRIMARY KEY);
		CREATE TABLE child (id TEXT PRIMARY KEY, parent_id TEXT REFERENCES parent(id));
	`); err != nil {
		t.Fatal(err)
	}

	// A clean graph passes.
	cleanTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkForeignKeys(ctx, cleanTx, "catalog", "0099_clean.sql"); err != nil {
		t.Fatalf("checkForeignKeys flagged a clean graph: %v", err)
	}
	_ = cleanTx.Rollback()

	// Land an orphan, then assert the guard names the migration, table, parent.
	if _, err := db.ExecContext(ctx, `INSERT INTO child(id, parent_id) VALUES ('c1', 'missing-parent')`); err != nil {
		t.Fatal(err)
	}
	badTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer badTx.Rollback()
	err = checkForeignKeys(ctx, badTx, "catalog", "0100_rebuild.sql")
	if err == nil {
		t.Fatal("checkForeignKeys passed a database with a dangling foreign key")
	}
	for _, want := range []string{"0100_rebuild.sql", "foreign-key violation", `"child"`, `"parent"`} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err, want)
		}
	}
}

func TestTimestampMigrationConvertsSecondsToMillis(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "catalog.db")
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	backupDir := filepath.Join(t.TempDir(), "backups")

	// Apply only the pre-millisecond catalog migrations, then seed seconds rows.
	preMS := migrationsBefore(t, catalogMigrations, "migrate/catalog", "0005")
	if err := migrate(ctx, slog.Default(), db, dbPath, "catalog", preMS, "migrate/catalog", backupDir); err != nil {
		t.Fatalf("pre-ms migrate returned error: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO library(id, title) VALUES ('library-local', 'Local');
		INSERT INTO profile(id, name, kind) VALUES ('profile-default', 'Default', 'adult');
		INSERT INTO pairing_code(code, profile_id, device_role, expires_at, consumed_at)
		  VALUES ('PAIRSECS', 'profile-default', 'viewer', 600, NULL);
		INSERT INTO media(id, library_id, title, duration_seconds, container, codec)
		  VALUES ('media-old', 'library-local', 'Old', 60, 'mp4', 'h264');
		INSERT INTO media_file(content_fingerprint, media_id, path, missing_at, last_seen_at)
		  VALUES ('sha256:old', 'media-old', '/media/old.mp4', NULL, 700);
	`); err != nil {
		t.Fatal(err)
	}

	if err := migrate(ctx, slog.Default(), db, dbPath, "catalog", catalogMigrations, "migrate/catalog", backupDir); err != nil {
		t.Fatalf("full migrate returned error: %v", err)
	}

	var expiresMS int64
	if err := db.QueryRowContext(ctx, "SELECT expires_at_ms FROM pairing_code WHERE code = 'PAIRSECS'").Scan(&expiresMS); err != nil {
		t.Fatal(err)
	}
	if expiresMS != 600_000 {
		t.Fatalf("expires_at_ms = %d, want 600000", expiresMS)
	}
	var lastSeenMS int64
	if err := db.QueryRowContext(ctx, "SELECT last_seen_at_ms FROM media_file WHERE content_fingerprint = 'sha256:old'").Scan(&lastSeenMS); err != nil {
		t.Fatal(err)
	}
	if lastSeenMS != 700_000 {
		t.Fatalf("last_seen_at_ms = %d, want 700000", lastSeenMS)
	}
	// 0052 (Versions/Parts) rebuilt media_file: the pre-existing single-file row
	// must survive into the default Version ("" label, ordinal 0) with its id
	// seeded from content_fingerprint — invariant #6, nothing silently dropped.
	var id, versionLabel string
	var partOrdinal int
	if err := db.QueryRowContext(ctx,
		"SELECT id, version_label, part_ordinal FROM media_file WHERE content_fingerprint = 'sha256:old'",
	).Scan(&id, &versionLabel, &partOrdinal); err != nil {
		t.Fatal(err)
	}
	if id != "sha256:old" || versionLabel != "" || partOrdinal != 0 {
		t.Fatalf("migrated row = {id:%q version_label:%q part_ordinal:%d}, want {id:\"sha256:old\" version_label:\"\" part_ordinal:0}", id, versionLabel, partOrdinal)
	}
}

func TestFeedGUIDSchemeMigrationRewritesLegacyRows(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "catalog.db")
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	backupDir := filepath.Join(t.TempDir(), "backups")

	beforeFeedGUID := migrationsBefore(t, catalogMigrations, "migrate/catalog", "0087")
	if err := migrate(ctx, slog.Default(), db, dbPath, "catalog", beforeFeedGUID, "migrate/catalog", backupDir); err != nil {
		t.Fatalf("pre-feed-guid migrate returned error: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO library(id, title) VALUES ('library-local', 'Local');
		INSERT INTO media(id, library_id, title, duration_seconds, container, codec)
		  VALUES ('media-old', 'library-local', 'Old', 60, 'mp3', 'mp3'),
		         ('media-conflict', 'library-local', 'Conflict', 60, 'mp3', 'mp3');
		INSERT INTO media_external_id(media_id, scheme, value)
		  VALUES ('media-old', 'podcast-guid', 'guid-old'),
		         ('media-conflict', 'podcast-guid', 'guid-conflict'),
		         ('media-conflict', 'feed-guid', 'guid-conflict');
	`); err != nil {
		t.Fatal(err)
	}

	if err := migrate(ctx, slog.Default(), db, dbPath, "catalog", catalogMigrations, "migrate/catalog", backupDir); err != nil {
		t.Fatalf("full migrate returned error: %v", err)
	}
	var oldRows, feedRows int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_external_id WHERE scheme = 'podcast-guid'`).Scan(&oldRows); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_external_id WHERE scheme = 'feed-guid'`).Scan(&feedRows); err != nil {
		t.Fatal(err)
	}
	if oldRows != 0 || feedRows != 2 {
		t.Fatalf("scheme rows after migration: podcast-guid=%d feed-guid=%d, want 0/2", oldRows, feedRows)
	}
}

// migrationsBefore copies embedded migration files whose names sort below stop.
func migrationsBefore(t *testing.T, source fs.FS, dir string, stop string) fstest.MapFS {
	t.Helper()
	entries, err := fs.ReadDir(source, dir)
	if err != nil {
		t.Fatal(err)
	}
	subset := fstest.MapFS{}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() >= stop {
			continue
		}
		data, err := fs.ReadFile(source, filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		subset[filepath.Join(dir, entry.Name())] = &fstest.MapFile{Data: data}
	}
	return subset
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var count int
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", name).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count != 0
}

func columnExists(t *testing.T, db *sql.DB, tableName, columnName string) bool {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), "PRAGMA table_info("+quoteSQLiteString(tableName)+")")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			t.Fatal(err)
		}
		if name == columnName {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return false
}

func replaceFilesystemDetector(detector func(string) (dataDirFilesystem, error)) func() {
	original := detectDataDirFilesystem
	detectDataDirFilesystem = detector
	return func() {
		detectDataDirFilesystem = original
	}
}

func TestOpenRefusesNewerSchema(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(context.Background(), dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	catalogPath := st.catalogPath
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	// One beyond the current catalog migration count: a hardcoded number would
	// silently stop testing "newer" the moment the count reached it.
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", schemaFreezeCatalogMigrations+1)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = Open(context.Background(), dir, Options{})
	if err == nil {
		t.Fatal("Open accepted a database from a newer binary")
	}
	if !strings.Contains(err.Error(), "newer than this binary supports") {
		t.Fatalf("error = %q, want the documented refuse-newer message", err)
	}
}

func TestPendingMigrationsBackupBeforeApplying(t *testing.T) {
	dir := t.TempDir()
	backupDir := filepath.Join(dir, "backups", "pre-migration")
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		t.Fatal(err)
	}
	backupRoot, err := os.OpenRoot(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	chmodErr := backupRoot.Chmod(".", 0o755)
	closeErr := backupRoot.Close()
	if chmodErr != nil {
		t.Fatal(chmodErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	st, err := Open(context.Background(), dir, Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// The first open migrates 0 -> current, and the runner writes one session
	// snapshot containing every database before applying anything to any of them.
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("backup dir missing after migration: %v", err)
	}
	backupInfo, err := os.Stat(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := backupInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("backup dir mode = %o, want 700", got)
	}
	snapshots, err := migrationSnapshots(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("migration session snapshots = %v, want exactly one", snapshots)
	}
	manifest := snapshots[0].manifest
	if manifest.FromVersions["catalog"] != 0 || manifest.FromVersions["events"] != 0 {
		t.Fatalf("pre-migration versions = %v, want both databases at version 0", manifest.FromVersions)
	}
	for _, name := range []string{"catalog", "events"} {
		snapshot, ok := manifest.Databases[name]
		if !ok || snapshot.BackupPath != filepath.Join(snapshots[0].path, name+".db") {
			t.Fatalf("%s migration snapshot = %#v", name, snapshot)
		}
		if _, err := os.Stat(snapshot.BackupPath); err != nil {
			t.Fatalf("stat %s migration snapshot: %v", name, err)
		}
	}

	// A second open has nothing pending: no new backups are written.
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(context.Background(), dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(entries) {
		t.Fatalf("backup count changed %d -> %d on a no-op open", len(entries), len(after))
	}

	// And the catalog survived intact.
	media, err := st.Media(context.Background(), "media-alpha", LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil || media.Title != "Sample Media" {
		t.Fatalf("seeded media after re-open = %#v err=%v", media, err)
	}
}

func TestSearchMediaMatchesPrefixesAndFiltersReveal(t *testing.T) {
	st, err := Open(context.Background(), t.TempDir(), Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	hidden := LibraryFilter{AllowedIDs: []LibraryID{"library-local", "library-archive"}}
	items, err := st.SearchMedia(context.Background(), hidden, "samp", 10)
	if err != nil {
		t.Fatalf("SearchMedia returned error: %v", err)
	}
	if len(items) != 1 || items[0].ID != "media-alpha" {
		t.Fatalf("search results = %#v, want media-alpha by prefix", items)
	}

	// Restricted-Library media stays invisible until reveal.
	items, err = st.SearchMedia(context.Background(), hidden, "archive", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("unrevealed search found restricted media: %#v", items)
	}
	revealed := LibraryFilter{AllowedIDs: hidden.AllowedIDs, Revealed: true}
	items, err = st.SearchMedia(context.Background(), revealed, "archive", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "media-archive" {
		t.Fatalf("revealed search = %#v, want media-archive", items)
	}

	// Scanned (trigger-indexed) media is searchable too.
	if _, _, err := st.ImportMediaFile(context.Background(), ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/tmp/beamfall-fts-test.mp4",
		Title:              "Voyage Home",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "sha256:fts-test",
	}); err != nil {
		t.Fatal(err)
	}
	items, err = st.SearchMedia(context.Background(), hidden, "voy", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Title != "Voyage Home" {
		t.Fatalf("search after import = %#v, want Voyage Home", items)
	}

	// FTS operators in user text are inert.
	if _, err := st.SearchMedia(context.Background(), hidden, `sample" OR media_fts NEAR(`, 10); err != nil {
		t.Fatalf("operator-laden query errored: %v", err)
	}
}

func TestSearchMediaFoldsDiacriticsTyposAndTextFields(t *testing.T) {
	st, err := Open(context.Background(), t.TempDir(), Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if _, err := st.catalogW.ExecContext(ctx, `UPDATE library SET plugin_id = 'photos' WHERE id = 'library-local'`); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceMediaFieldSearchPolicies(ctx, []MediaFieldSearchPolicy{{
		PluginID: "photos", Field: "album", Type: EntityFieldText, Searchable: true,
	}}); err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/tmp/beyonce.flac",
		Title:              "Beyoncé - Halo",
		Container:          "flac",
		Codec:              "flac",
		ContentFingerprint: "sha256:beyonce-halo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetMediaFields(ctx, media.ID, []MediaField{
		{Name: "album", Type: EntityFieldText, Text: "I Am... Sasha Fierce"},
	}); err != nil {
		t.Fatal(err)
	}
	filter := LibraryFilter{AllowedIDs: []LibraryID{"library-local", "library-archive"}}
	for _, query := range []string{"beyonce", "halp", "sasha fierc"} {
		items, err := st.SearchMedia(ctx, filter, query, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) == 0 || items[0].ID != media.ID {
			t.Fatalf("search %q = %#v, want %s first", query, items, media.ID)
		}
	}
}

func TestTrashPurgeRemovesOnlyExpiredRows(t *testing.T) {
	st, err := Open(context.Background(), t.TempDir(), Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	filter := LibraryFilter{Revealed: true, AllLibraries: true}

	if err := st.TrashMedia(context.Background(), "media-alpha", filter); err != nil {
		t.Fatal(err)
	}
	// Fresh trash survives a purge at the retention cutoff.
	purged, err := st.PurgeTrashedBefore(context.Background(), time.Now().UTC().Add(-TrashRetention))
	if err != nil {
		t.Fatal(err)
	}
	if purged != 0 {
		t.Fatalf("purged %d fresh entries, want 0", purged)
	}
	// A future cutoff purges it; the row and its files are gone.
	purged, err = st.PurgeTrashedBefore(context.Background(), time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if purged != 1 {
		t.Fatalf("purged = %d, want 1", purged)
	}
	if _, err := st.Media(context.Background(), "media-alpha", filter); err == nil {
		t.Fatal("purged media still readable")
	}
	entries, err := st.TrashedMediaList(context.Background(), filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("trash after purge = %#v, want empty", entries)
	}
	activity, err := st.ActivityEntries(context.Background(), ActivityQuery{Filter: filter, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(activity) == 0 || activity[0].Action != ActivityTrashPurge || activity[0].TargetID != "1" {
		t.Fatalf("latest activity = %#v, want trash purge affected=1", activity)
	}
}

func TestTrashPurgeSweepTrashDisabledRequiresConfirmation(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	now := time.Now().UTC()
	cutoff := now.Add(-TrashRetention)
	filter := LibraryFilter{Revealed: true, AllLibraries: true}

	st, err := Open(ctx, dir, Options{SeedDevelopmentData: true, TrashDisabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.trashMediaAt(ctx, "media-alpha", filter, cutoff.Add(-time.Hour)); err != nil {
		t.Fatalf("trash old media: %v", err)
	}
	if err := st.trashMediaAt(ctx, "media-archive", filter, now); err != nil {
		t.Fatalf("trash fresh media: %v", err)
	}
	purged, skipped, err := st.SweepPurgeTrashedBefore(ctx, cutoff)
	if !errors.Is(err, ErrTrashDisabledConfirmationRequired) || !skipped || purged != 0 {
		t.Fatalf("disabled sweep purged=%d skipped=%v err=%v, want confirmation required", purged, skipped, err)
	}
	if got := st.TrashDisabledSweepSkips(); got != 1 {
		t.Fatalf("TrashDisabledSweepSkips = %d, want 1", got)
	}
	if entries, err := st.TrashedMediaList(ctx, filter); err != nil || len(entries) != 2 {
		t.Fatalf("trash after disabled sweep = %#v err=%v, want 2 retained", entries, err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st, err = Open(ctx, dir, Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	purged, skipped, err = st.SweepPurgeTrashedBefore(ctx, cutoff)
	if err != nil || skipped || purged != 1 {
		t.Fatalf("enabled sweep purged=%d skipped=%v err=%v, want 1 false nil", purged, skipped, err)
	}
	if _, err := st.Media(ctx, "media-alpha", filter); err == nil {
		t.Fatal("expired trashed media survived enabled sweep")
	}
	if entries, err := st.TrashedMediaList(ctx, filter); err != nil || len(entries) != 1 || entries[0].ID != "media-archive" {
		t.Fatalf("trash after enabled sweep = %#v err=%v, want only fresh media-archive", entries, err)
	}
}

func TestPurgeTrashedBeforeFailsAfterActivityAppendFailure(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	filter := LibraryFilter{Revealed: true, AllLibraries: true}
	if err := st.TrashMedia(ctx, "media-alpha", filter); err != nil {
		t.Fatalf("TrashMedia: %v", err)
	}

	if err := st.eventsW.Close(); err != nil {
		t.Fatalf("close events writer: %v", err)
	}

	purged, err := st.PurgeTrashedBefore(ctx, time.Now().UTC().Add(time.Hour))
	if err == nil || purged != 1 {
		t.Fatalf("PurgeTrashedBefore purged=%d err=%v, want 1 and audit error", purged, err)
	}
	if _, err := st.Media(ctx, "media-alpha", filter); err == nil {
		t.Fatal("purged Media is still readable")
	}
}

// mediaFieldFilterSQL errors on malformed echoes rather than silently dropping
// them: a dropped predicate would widen a narrowed shelf back to the whole
// catalog (the same Reveal-contract rationale that makes a partial entityFilter
// an error). Well-formed typed values — text/number/flag/date equality and
// gte/lte ranges — compose without error.
func TestMediaFieldFilterSQLMalformedValues(t *testing.T) {
	good := []struct {
		name    string
		filters map[string]any
	}{
		{"text equality", map[string]any{"phase": "flight"}},
		{"number equality", map[string]any{"score": 8.5}},
		{"flag equality", map[string]any{"verified": true}},
		{"date equality (RFC3339)", map[string]any{"released": "2020-07-04T12:00:00Z"}},
		{"date equality (date only)", map[string]any{"released": "2020-07-04"}},
		{"number range", map[string]any{"score": map[string]any{"gte": 4, "lte": 5}}},
		{"date range", map[string]any{"released": map[string]any{"gte": "2020-01-01"}}},
	}
	for _, tc := range good {
		if _, _, err := mediaFieldFilterSQL(tc.filters); err != nil {
			t.Fatalf("%s: unexpected error %v", tc.name, err)
		}
	}

	bad := []struct {
		name    string
		filters map[string]any
	}{
		{"empty text", map[string]any{"phase": "   "}},
		{"unsupported scalar type", map[string]any{"score": []int{1, 2}}},
		{"unknown range key", map[string]any{"score": map[string]any{"between": 4}}},
		{"empty range object", map[string]any{"score": map[string]any{}}},
		{"non-numeric range bound", map[string]any{"score": map[string]any{"gte": "high"}}},
	}
	for _, tc := range bad {
		if _, _, err := mediaFieldFilterSQL(tc.filters); err == nil {
			t.Fatalf("%s: want error, got nil", tc.name)
		}
	}
}
