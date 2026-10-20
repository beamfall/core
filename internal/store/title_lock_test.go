package store

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
)

func TestIdentifiedTitleSurvivesReimport(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	imported := ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/media/sample-file.mp4",
		Title:              "sample-file",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "fp-identified-title",
	}
	media, _, err := st.ImportMediaFile(ctx, imported)
	if err != nil {
		t.Fatal(err)
	}

	identified := "Proper Title"
	if err := st.UpdateMediaMetadata(ctx, media.ID, MediaMetadata{Title: &identified}); err != nil {
		t.Fatal(err)
	}

	// Re-scan of the same file: the filename-derived title must not clobber
	// the identified one, and the import reports stored truth.
	again, _, err := st.ImportMediaFile(ctx, imported)
	if err != nil {
		t.Fatal(err)
	}
	if again.Title != identified {
		t.Fatalf("reimported title = %q, want %q", again.Title, identified)
	}
	stored, err := st.Media(ctx, media.ID, LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if stored.Title != identified {
		t.Fatalf("stored title = %q, want %q", stored.Title, identified)
	}

	// A never-identified row keeps following the file on re-import.
	plain := ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/media/other-file.mp4",
		Title:              "other-file",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "fp-plain-title",
	}
	if _, _, err := st.ImportMediaFile(ctx, plain); err != nil {
		t.Fatal(err)
	}
	plain.Title = "other-file-renamed"
	renamed, _, err := st.ImportMediaFile(ctx, plain)
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Title != "other-file-renamed" {
		t.Fatalf("unidentified reimport title = %q, want other-file-renamed", renamed.Title)
	}
}

func TestIdentifiedSummaryAndYearSurviveReimport(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	imported := ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/media/field-locks.mp4",
		Title:              "field-locks",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "fp-identified-summary-year",
	}
	media, _, err := st.ImportMediaFile(ctx, imported)
	if err != nil {
		t.Fatal(err)
	}

	identifiedSummary := "Identified summary."
	identifiedYear := 1998
	if err := st.UpdateMediaMetadata(ctx, media.ID, MediaMetadata{
		Summary: &identifiedSummary,
		Year:    &identifiedYear,
	}); err != nil {
		t.Fatal(err)
	}

	if _, _, err := st.ImportMediaFile(ctx, imported); err != nil {
		t.Fatal(err)
	}
	stored, err := st.Media(ctx, media.ID, LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if stored.Summary != identifiedSummary {
		t.Fatalf("stored summary = %q, want %q", stored.Summary, identifiedSummary)
	}
	if stored.Year != identifiedYear {
		t.Fatalf("stored year = %d, want %d", stored.Year, identifiedYear)
	}

	var summaryLock, yearLock int
	if err := st.catalogR.QueryRowContext(ctx,
		"SELECT summary_identified, year_identified FROM media WHERE id = ?",
		media.ID,
	).Scan(&summaryLock, &yearLock); err != nil {
		t.Fatal(err)
	}
	if summaryLock != 1 || yearLock != 1 {
		t.Fatalf("metadata locks = summary:%d year:%d, want both 1", summaryLock, yearLock)
	}
}

// TestImportedSummarySurvivesEmptyReimport covers the MIG-2 summary
// preservation CASE: an import-supplied summary (no identification lock set)
// must not be erased by a later re-scan that carries no summary. Mirrors the
// title/year empty-never-clobbers shape — a re-scan without metadata preserves
// the prior import/edit rather than blanking it.
func TestImportedSummarySurvivesEmptyReimport(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	withSummary := ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/media/import-summary.mp4",
		Title:              "import-summary",
		Summary:            "Imported summary from a sidecar.",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "fp-import-summary",
	}
	media, _, err := st.ImportMediaFile(ctx, withSummary)
	if err != nil {
		t.Fatal(err)
	}
	if media.Summary != withSummary.Summary {
		t.Fatalf("first import summary = %q, want %q", media.Summary, withSummary.Summary)
	}

	// A re-scan with NO summary (the common probe-less rescan) must preserve it.
	bare := withSummary
	bare.Summary = ""
	again, _, err := st.ImportMediaFile(ctx, bare)
	if err != nil {
		t.Fatal(err)
	}
	if again.Summary != withSummary.Summary {
		t.Fatalf("reimport with empty summary = %q, want preserved %q", again.Summary, withSummary.Summary)
	}
	stored, err := st.Media(ctx, media.ID, LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if stored.Summary != withSummary.Summary {
		t.Fatalf("stored summary = %q, want preserved %q", stored.Summary, withSummary.Summary)
	}

	// A re-scan that DOES carry a new summary (and no identification lock) still
	// updates — the gap-fill / advance arm of the CASE.
	bare.Summary = "A newer scanned summary."
	updated, _, err := st.ImportMediaFile(ctx, bare)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Summary != "A newer scanned summary." {
		t.Fatalf("reimport with new summary = %q, want the new value", updated.Summary)
	}
}

func TestMetadataLocksArePerField(t *testing.T) {
	// feature:field-locks
	st := openSeeded(t)
	ctx := context.Background()
	imported := ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/media/per-field-locks.mp4",
		Title:              "per-field-locks",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "fp-per-field-locks",
	}
	media, _, err := st.ImportMediaFile(ctx, imported)
	if err != nil {
		t.Fatal(err)
	}

	identifiedSummary := "Locked summary."
	if err := st.UpdateMediaMetadata(ctx, media.ID, MediaMetadata{Summary: &identifiedSummary}); err != nil {
		t.Fatal(err)
	}

	var summaryLock, yearLock int
	if err := st.catalogR.QueryRowContext(ctx,
		"SELECT summary_identified, year_identified FROM media WHERE id = ?",
		media.ID,
	).Scan(&summaryLock, &yearLock); err != nil {
		t.Fatal(err)
	}
	if summaryLock != 1 || yearLock != 0 {
		t.Fatalf("metadata locks after summary update = summary:%d year:%d, want 1/0", summaryLock, yearLock)
	}

	identifiedYear := 2026
	if err := st.UpdateMediaMetadata(ctx, media.ID, MediaMetadata{Year: &identifiedYear}); err != nil {
		t.Fatal(err)
	}
	if err := st.catalogR.QueryRowContext(ctx,
		"SELECT summary_identified, year_identified FROM media WHERE id = ?",
		media.ID,
	).Scan(&summaryLock, &yearLock); err != nil {
		t.Fatal(err)
	}
	if summaryLock != 1 || yearLock != 1 {
		t.Fatalf("metadata locks after year update = summary:%d year:%d, want 1/1", summaryLock, yearLock)
	}
}

func TestSummaryYearIdentifiedMigrationDefaultsExistingRowsToUnlocked(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "catalog.db")
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	beforeLocks := migrationsBefore(t, catalogMigrations, "migrate/catalog", "0028")
	if err := migrate(ctx, slog.Default(), db, dbPath, "catalog", beforeLocks, "migrate/catalog", filepath.Join(t.TempDir(), "backups")); err != nil {
		t.Fatalf("pre-lock migrate returned error: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO library(id, title) VALUES ('library-local', 'Local');
		INSERT INTO media(id, library_id, title, summary, year, duration_seconds, container, codec, media_class)
		  VALUES ('media-existing', 'library-local', 'Existing', 'Existing summary.', 2001, 60, 'mp4', 'h264', 'video');
	`); err != nil {
		t.Fatal(err)
	}

	if err := migrate(ctx, slog.Default(), db, dbPath, "catalog", catalogMigrations, "migrate/catalog", filepath.Join(t.TempDir(), "backups")); err != nil {
		t.Fatalf("full migrate returned error: %v", err)
	}
	var summaryLock, yearLock int
	if err := db.QueryRowContext(ctx,
		"SELECT summary_identified, year_identified FROM media WHERE id = 'media-existing'",
	).Scan(&summaryLock, &yearLock); err != nil {
		t.Fatal(err)
	}
	if summaryLock != 0 || yearLock != 0 {
		t.Fatalf("metadata locks = summary:%d year:%d, want both 0", summaryLock, yearLock)
	}
}
