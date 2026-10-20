package store

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEncryptedMediaFieldWritersStayInProtectedPlane(t *testing.T) {
	ctx := context.Background()
	st, media := seededEncryptedMedia(t, "encrypted-fields")
	defer st.Close()

	var outboxBefore int
	if err := st.eventsR.QueryRowContext(ctx, `SELECT COUNT(*) FROM event_outbox WHERE aggregate_id = ?`, media.ID).Scan(&outboxBefore); err != nil {
		t.Fatal(err)
	}
	date := time.Date(2021, 7, 14, 0, 0, 0, 0, time.UTC)
	if err := st.SetMediaFields(ctx, media.ID, []MediaField{
		{Name: "caption", Type: EntityFieldText, Text: "Paris"},
		{Name: "score", Type: EntityFieldNumber, Number: 4.5},
		{Name: "favorite", Type: EntityFieldFlag, Flag: true},
		{Name: "capture-date", Type: EntityFieldDate, Date: date},
	}); err != nil {
		t.Fatalf("SetMediaFields: %v", err)
	}
	if err := st.UpsertMediaFieldsWithSource(ctx, media.ID, []MediaField{
		{Name: "caption", Type: EntityFieldText, Text: "operator"},
	}, MutationSourceOperator); err != nil {
		t.Fatalf("UpsertMediaFieldsWithSource: %v", err)
	}
	if err := st.UpsertMediaFieldsWithSource(ctx, media.ID, []MediaField{
		{Name: "caption", Type: EntityFieldText, Text: "scan"},
	}, MutationSourceScan); err != nil {
		t.Fatalf("lower-precedence UpsertMediaFieldsWithSource: %v", err)
	}
	if err := st.InsertMediaFieldsIfAbsent(ctx, media.ID, []MediaField{
		{Name: "caption", Type: EntityFieldText, Text: "insert-noop"},
		{Name: "album", Type: EntityFieldText, Text: "Summer"},
	}); err != nil {
		t.Fatalf("InsertMediaFieldsIfAbsent: %v", err)
	}

	fields, err := st.MediaFields(ctx, media.ID, LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := fieldText(fields, "caption"); got != "operator" {
		t.Fatalf("caption = %q, want operator precedence winner", got)
	}
	if got, _ := fieldText(fields, "album"); got != "Summer" {
		t.Fatalf("album = %q, want inserted value", got)
	}
	for _, table := range mediaFieldTables {
		var count int
		if err := st.catalogR.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE media_id = ?`, media.ID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("plaintext %s rows = %d, want 0", table, count)
		}
	}
	var outboxAfter int
	if err := st.eventsR.QueryRowContext(ctx, `SELECT COUNT(*) FROM event_outbox WHERE aggregate_id = ?`, media.ID).Scan(&outboxAfter); err != nil {
		t.Fatal(err)
	}
	if outboxAfter != outboxBefore {
		t.Fatalf("encrypted field writes emitted plaintext outbox rows: before=%d after=%d", outboxBefore, outboxAfter)
	}
}

func TestEncryptedMediaFieldTypedTombstoneAndSeal(t *testing.T) {
	ctx := context.Background()
	st, media := seededEncryptedMedia(t, "encrypted-tombstone")
	defer st.Close()

	value := MediaField{Name: "mood", Type: EntityFieldText, Text: "noir"}
	if err := st.UpsertMediaFieldsWithSource(ctx, media.ID, []MediaField{value}, MutationSourceOperator); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMediaFields(ctx, media.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertMediaFieldsWithSource(ctx, media.ID, []MediaField{value}, MutationSourceOperator); err != nil {
		t.Fatal(err)
	}
	fields, err := st.MediaFields(ctx, media.ID, LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if hasField(fields, "mood") {
		t.Fatalf("manual-cleared field reappeared: %#v", fields)
	}

	db := st.encryptedDBs[media.LibraryID].reader
	var fieldType string
	if err := db.QueryRowContext(ctx, `
		SELECT field_type FROM media_field_rejection
		WHERE media_id = ? AND field = 'mood' AND value = 'noir'`, media.ID).Scan(&fieldType); err != nil {
		t.Fatal(err)
	}
	if fieldType != string(EntityFieldText) {
		t.Fatalf("rejection field_type = %q, want %q", fieldType, EntityFieldText)
	}

	if err := st.SealEncryptedLibraryDB(ctx, media.LibraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertMediaFields(ctx, media.ID, []MediaField{{Name: "mood", Type: EntityFieldText, Text: "sealed"}}); err == nil {
		t.Fatal("sealed encrypted media accepted a field write")
	}
}

func TestEncryptedMediaFieldMigrationBackfillAndFTSTriggers(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "encrypted.db")
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	before := migrationsBefore(t, encryptedLibraryMigrations, "migrate/encrypted_library", "0004")
	if err := migrate(ctx, slog.Default(), db, dbPath, encryptedLibraryDBLabel, before, "migrate/encrypted_library", filepath.Join(t.TempDir(), "backups")); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO media(id, library_id, title, duration_seconds, container, codec, media_class,
			title_identified, summary_identified, year_identified)
		VALUES ('media-upgrade', 'library-encrypted', 'Upgrade', 1, 'jpg', 'jpeg', 'image', 1, 1, 1)
	`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, slog.Default(), db, dbPath, encryptedLibraryDBLabel, encryptedLibraryMigrations, "migrate/encrypted_library", filepath.Join(t.TempDir(), "backups")); err != nil {
		t.Fatal(err)
	}
	var provenance int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_field_provenance WHERE media_id = 'media-upgrade' AND source = 'operator'`).Scan(&provenance); err != nil {
		t.Fatal(err)
	}
	if provenance != 3 {
		t.Fatalf("identified provenance rows = %d, want 3", provenance)
	}

	if _, err := db.ExecContext(ctx, `INSERT INTO media_field_text(media_id, field, field_type, value) VALUES ('media-upgrade', 'place', 'text', 'Paris')`); err != nil {
		t.Fatal(err)
	}
	assertEncryptedFieldFTSCount(t, db, "Paris", 1)
	if _, err := db.ExecContext(ctx, `UPDATE media_field_text SET value = 'Toronto' WHERE media_id = 'media-upgrade' AND field = 'place'`); err != nil {
		t.Fatal(err)
	}
	assertEncryptedFieldFTSCount(t, db, "Paris", 0)
	assertEncryptedFieldFTSCount(t, db, "Toronto", 1)
	if _, err := db.ExecContext(ctx, `DELETE FROM media_field_text WHERE media_id = 'media-upgrade' AND field = 'place'`); err != nil {
		t.Fatal(err)
	}
	assertEncryptedFieldFTSCount(t, db, "Toronto", 0)
}

func assertEncryptedFieldFTSCount(t *testing.T, db *sql.DB, query string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM media_field_fts WHERE media_field_fts MATCH ?`, query).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("FTS %q count = %d, want %d", query, got, want)
	}
}

func TestMediaLackingFieldInEncryptedLibraryUsesProtectedPlane(t *testing.T) {
	ctx := context.Background()
	libraryID := LibraryID("library-local")
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.SetLibraryBacking(ctx, libraryID, false, true); err != nil {
		t.Fatal(err)
	}
	if _, err := st.MediaLackingFieldInLibrary(ctx, libraryID, MediaClassAudio, "hydrated"); !errors.Is(err, ErrEncryptedLibraryDBDisabled) {
		t.Fatalf("sealed query error = %v, want encrypted DB disabled", err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatal(err)
	}
	item := encryptedImport(libraryID, "sha256:encrypted-hydration", "/encrypted/hydration.flac")
	item.Class, item.Container, item.Codec = MediaClassAudio, "flac", "flac"
	media, _, err := st.ImportMediaFile(ctx, item)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := st.MediaLackingFieldInLibrary(ctx, libraryID, MediaClassAudio, "hydrated")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := stale[media.ID]; !ok || len(stale) != 1 {
		t.Fatalf("unsealed stale set = %v, want only %s", stale, media.ID)
	}
	if err := st.InsertMediaFieldsIfAbsent(ctx, media.ID, []MediaField{{Name: "hydrated", Type: EntityFieldFlag, Flag: true}}); err != nil {
		t.Fatal(err)
	}
	stale, err = st.MediaLackingFieldInLibrary(ctx, libraryID, MediaClassAudio, "hydrated")
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 0 {
		t.Fatalf("marked encrypted media remained stale: %v", stale)
	}
	if err := st.SealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.MediaLackingFieldInLibrary(ctx, libraryID, MediaClassAudio, "hydrated"); !errors.Is(err, ErrEncryptedLibraryDBDisabled) {
		t.Fatalf("re-sealed query error = %v, want encrypted DB disabled", err)
	}
}

func TestEncryptedLibraryUnsealCleanupFailureQuarantines(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	libraryID := LibraryID("library-local")
	if err := st.SetLibraryBacking(ctx, libraryID, false, true); err != nil {
		t.Fatal(err)
	}
	reconcileErr := errors.New("injected reconcile failure")
	cleanupErr := errors.New("injected cleanup failure")
	st.SetEncryptedDerivativeReconciler(&recordingDerivativeReconciler{failErr: reconcileErr})
	originalCleanup := cleanupEncryptedLibraryWorkingDir
	cleanupEncryptedLibraryWorkingDir = func(string, string) error { return cleanupErr }
	err = st.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{})
	cleanupEncryptedLibraryWorkingDir = originalCleanup
	if !errors.Is(err, reconcileErr) || !errors.Is(err, cleanupErr) {
		t.Fatalf("unseal error = %v, want joined reconcile and cleanup errors", err)
	}
	reason, quarantined := st.EncryptedLibraryDBQuarantine(libraryID)
	if !quarantined || !strings.Contains(reason, cleanupErr.Error()) {
		t.Fatalf("quarantine = (%q, %v), want cleanup failure", reason, quarantined)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, copyEncryptedLibraryDBCipher{}); !errors.Is(err, ErrEncryptedLibraryDBQuarantined) {
		t.Fatalf("second unseal error = %v, want quarantine", err)
	}
	if err := st.RepairEncryptedLibraryDBQuarantine(ctx, libraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("repair cleanup quarantine: %v", err)
	}
	if _, quarantined := st.EncryptedLibraryDBQuarantine(libraryID); quarantined {
		t.Fatal("cleanup quarantine remained after repair")
	}
}
