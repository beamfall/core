package store

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"
)

// feature:field-locks

func provenanceSource(t *testing.T, st *Store, id MediaID, field string) string {
	t.Helper()
	var source string
	err := st.catalogR.QueryRowContext(context.Background(),
		"SELECT source FROM media_field_provenance WHERE media_id = ? AND field = ?", id, field,
	).Scan(&source)
	if err != nil {
		t.Fatalf("provenance read (%s): %v", field, err)
	}
	return source
}

// TestManualEditSurvivesReidentify is the METAPARITY-3 core guarantee: a manual
// edit auto-locks the built-in field (source=manual) and a subsequent operator
// re-identify cannot overwrite it — the source-precedence record subsumes the
// legacy *_identified boolean, which stays set.
func TestManualEditSurvivesReidentify(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	imported := ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/media/manual-edit.mp4",
		Title:              "manual-edit",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "fp-manual-edit",
	}
	media, _, err := st.ImportMediaFile(ctx, imported)
	if err != nil {
		t.Fatal(err)
	}

	// Scan-tier provenance recorded on import.
	if got := provenanceSource(t, st, media.ID, "title"); got != "scan" {
		t.Fatalf("post-import title provenance = %q, want scan", got)
	}

	// A manual edit auto-locks at the manual tier.
	manualTitle := "Human-Chosen Title"
	if err := st.UpdateMediaMetadata(ctx, media.ID, MediaMetadata{Title: &manualTitle, Source: MutationSourceManual}); err != nil {
		t.Fatal(err)
	}
	if got := provenanceSource(t, st, media.ID, "title"); got != "manual" {
		t.Fatalf("post-edit title provenance = %q, want manual", got)
	}

	// An operator re-identify must NOT overwrite the manual title.
	operatorTitle := "Scraper Title"
	if err := st.UpdateMediaMetadata(ctx, media.ID, MediaMetadata{Title: &operatorTitle, Source: MutationSourceOperator}); err != nil {
		t.Fatal(err)
	}
	stored, err := st.Media(ctx, media.ID, LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if stored.Title != manualTitle {
		t.Fatalf("stored title = %q, want preserved %q", stored.Title, manualTitle)
	}
	if got := provenanceSource(t, st, media.ID, "title"); got != "manual" {
		t.Fatalf("title provenance after re-identify = %q, want still manual", got)
	}

	// The legacy boolean stays set — the record is authoritative, boolean subsumed.
	var titleLock int
	if err := st.catalogR.QueryRowContext(ctx,
		"SELECT title_identified FROM media WHERE id = ?", media.ID).Scan(&titleLock); err != nil {
		t.Fatal(err)
	}
	if titleLock != 1 {
		t.Fatalf("title_identified = %d, want 1 (subsumed lock stays set)", titleLock)
	}

	// A re-scan of the same file still cannot clobber the manual title.
	again, _, err := st.ImportMediaFile(ctx, imported)
	if err != nil {
		t.Fatal(err)
	}
	if again.Title != manualTitle {
		t.Fatalf("reimport title = %q, want preserved %q", again.Title, manualTitle)
	}
}

// TestPluginFieldPrecedence covers the plugin-field ((entity,field,source))
// record: an operator (identified) plugin field is preserved against a
// lower-tier scan write, and a manual bulk-edit field survives an operator
// re-identify.
func TestPluginFieldPrecedence(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	imported := ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/media/plugin-fields.mp4",
		Title:              "plugin-fields",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "fp-plugin-fields",
	}
	media, _, err := st.ImportMediaFile(ctx, imported)
	if err != nil {
		t.Fatal(err)
	}

	// Identification writes a plugin field at the operator tier.
	operatorField := []MediaField{{Name: "phase", Type: EntityFieldText, Text: "identified"}}
	if err := st.UpsertMediaFieldsWithSource(ctx, media.ID, operatorField, MutationSourceOperator); err != nil {
		t.Fatal(err)
	}
	if got := provenanceSource(t, st, media.ID, "phase"); got != "operator" {
		t.Fatalf("phase provenance = %q, want operator", got)
	}

	// A lower-tier scan write must NOT overwrite the operator value.
	scanField := []MediaField{{Name: "phase", Type: EntityFieldText, Text: "scanned"}}
	if err := st.UpsertMediaFieldsWithSource(ctx, media.ID, scanField, MutationSourceScan); err != nil {
		t.Fatal(err)
	}
	fields, err := st.MediaFields(ctx, media.ID, LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := fieldText(fields, "phase"); got != "identified" {
		t.Fatalf("phase after scan write = %q, want preserved identified", got)
	}

	// A manual bulk edit auto-locks the field, and a later operator re-identify
	// cannot overwrite it.
	if _, err := st.BulkEditApply(ctx, []MediaID{media.ID},
		[]MediaField{{Name: "phase", Type: EntityFieldText, Text: "human"}},
		LibraryFilter{Revealed: true, AllLibraries: true}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if got := provenanceSource(t, st, media.ID, "phase"); got != "manual" {
		t.Fatalf("phase provenance after bulk edit = %q, want manual", got)
	}
	if err := st.UpsertMediaFieldsWithSource(ctx, media.ID,
		[]MediaField{{Name: "phase", Type: EntityFieldText, Text: "re-identified"}}, MutationSourceOperator); err != nil {
		t.Fatal(err)
	}
	fields, err = st.MediaFields(ctx, media.ID, LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := fieldText(fields, "phase"); got != "human" {
		t.Fatalf("phase after re-identify = %q, want preserved human", got)
	}
}

func TestManualFieldClearTombstoneBlocksRefresh(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	if err := st.SetMediaFields(ctx, "media-alpha", []MediaField{{Name: "mood", Type: EntityFieldText, Text: "noir"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMediaFields(ctx, "media-alpha", nil); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertMediaFieldsIfAbsent(ctx, "media-alpha", []MediaField{{Name: "mood", Type: EntityFieldText, Text: "noir"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertMediaFieldsWithSource(ctx, "media-alpha",
		[]MediaField{{Name: "mood", Type: EntityFieldText, Text: "blue"}}, MutationSourceOperator); err != nil {
		t.Fatal(err)
	}
	fields, err := st.MediaFields(ctx, "media-alpha", LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if hasField(fields, "mood") {
		t.Fatalf("manual-cleared mood reappeared after refresh: %#v", fields)
	}
	var rejected int
	if err := st.catalogR.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM media_field_rejection
		WHERE media_id = 'media-alpha' AND field = 'mood' AND field_type = 'text' AND value = 'noir'`).Scan(&rejected); err != nil {
		t.Fatal(err)
	}
	if rejected != 1 {
		t.Fatalf("manual-clear tombstones = %d, want 1", rejected)
	}
}

func TestMachineRemovalTombstoneBlocksScraperRefresh(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	filter := LibraryFilter{Revealed: true, AllLibraries: true}
	run, err := st.BeginEnrichmentRun(ctx, "fixture-ai", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ProposePendingAITag(ctx, PendingAITag{
		ID: "pt-refresh-block", MediaID: "media-alpha", TagKind: "mood", Value: "noir", ProposedBy: "fixture-ai", RunID: run.ID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.PromotePendingAITagInRun(ctx, "pt-refresh-block", run.ID, filter); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RevertEnrichmentRun(ctx, run.ID, filter, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertMediaFieldsWithSource(ctx, "media-alpha",
		[]MediaField{{Name: "mood", Type: EntityFieldText, Text: "noir"}}, MutationSourceOperator); err != nil {
		t.Fatal(err)
	}
	fields, err := st.MediaFields(ctx, "media-alpha", filter)
	if err != nil {
		t.Fatal(err)
	}
	if hasField(fields, "mood") {
		t.Fatalf("tombstoned machine value reappeared after scraper refresh: %#v", fields)
	}
}

// TestMetadataSourceRankTotalOrder pins the precedence chain
// user/manual > identified > parsed > hydrated > machine-derived.
func TestMetadataSourceRankTotalOrder(t *testing.T) {
	order := []MutationSource{
		MutationSourceManual, MutationSourceOperator, MutationSourceScan,
		MutationSourceHydrated, MutationSourceAI,
	}
	for i := 1; i < len(order); i++ {
		if metadataSourceRank(order[i-1]) <= metadataSourceRank(order[i]) {
			t.Fatalf("rank(%s)=%d not > rank(%s)=%d", order[i-1], metadataSourceRank(order[i-1]), order[i], metadataSourceRank(order[i]))
		}
	}
	// An unknown/empty source ranks as parsed (the "" == scan reader rule).
	if metadataSourceRank("") != metadataSourceRank(MutationSourceScan) {
		t.Fatalf(`rank("")=%d, want == rank(scan)=%d`, metadataSourceRank(""), metadataSourceRank(MutationSourceScan))
	}
}

// TestProvenanceMigrationBackfillsIdentifiedRows checks 0095 backfills the
// (media, field, 'operator') provenance record from a pre-migration
// title_identified=1 row, and leaves unlocked rows without a provenance row.
func TestProvenanceMigrationBackfillsIdentifiedRows(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "catalog.db")
	db, err := openSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	before := migrationsBefore(t, catalogMigrations, "migrate/catalog", "0095")
	if err := migrate(ctx, slog.Default(), db, dbPath, "catalog", before, "migrate/catalog", filepath.Join(t.TempDir(), "backups")); err != nil {
		t.Fatalf("pre-0095 migrate returned error: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO library(id, title) VALUES ('library-local', 'Local');
		INSERT INTO media(id, library_id, title, summary, year, duration_seconds, container, codec, media_class, title_identified, summary_identified)
		  VALUES ('media-locked', 'library-local', 'Locked', 'Locked summary.', 2001, 60, 'mp4', 'h264', 'video', 1, 1);
		INSERT INTO media(id, library_id, title, summary, year, duration_seconds, container, codec, media_class)
		  VALUES ('media-plain', 'library-local', 'Plain', '', 0, 60, 'mp4', 'h264', 'video');
	`); err != nil {
		t.Fatal(err)
	}

	if err := migrate(ctx, slog.Default(), db, dbPath, "catalog", catalogMigrations, "migrate/catalog", filepath.Join(t.TempDir(), "backups")); err != nil {
		t.Fatalf("full migrate returned error: %v", err)
	}

	rows := map[string]string{}
	cursor, err := db.QueryContext(ctx, "SELECT field, source FROM media_field_provenance WHERE media_id = 'media-locked'")
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	for cursor.Next() {
		var field, source string
		if err := cursor.Scan(&field, &source); err != nil {
			t.Fatal(err)
		}
		rows[field] = source
	}
	if rows["title"] != "operator" || rows["summary"] != "operator" {
		t.Fatalf("backfilled provenance = %#v, want title/summary -> operator", rows)
	}
	if _, ok := rows["year"]; ok {
		t.Fatalf("year provenance backfilled but year_identified was 0: %#v", rows)
	}

	var plainCount int
	if err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM media_field_provenance WHERE media_id = 'media-plain'").Scan(&plainCount); err != nil {
		t.Fatal(err)
	}
	if plainCount != 0 {
		t.Fatalf("unlocked row got %d provenance rows, want 0", plainCount)
	}
}
