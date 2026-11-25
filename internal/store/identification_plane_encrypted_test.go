package store

import (
	"context"
	"database/sql"
	"slices"
	"testing"
	"time"
)

// CORE-0706: identification and retry state for an Encrypted Library's Media
// lives on that Library's plane, never in the shared catalog.
func TestEncryptedIdentificationStateStaysOnPrivatePlane(t *testing.T) {
	ctx := context.Background()
	st, media := seededEncryptedMedia(t, "encrypted-identify")
	defer st.Close()
	private := st.encryptedDBs[media.LibraryID].reader

	if err := st.AddMediaExternalID(ctx, media.ID, "example-db", "tt-42"); err != nil {
		t.Fatalf("AddMediaExternalID: %v", err)
	}
	ids, err := st.MediaExternalIDs(ctx, media.ID)
	if err != nil || len(ids) != 1 || ids[0].Value != "tt-42" {
		t.Fatalf("MediaExternalIDs = %#v, %v", ids, err)
	}
	found, err := st.MediaIDsByExternalID(ctx, "example-db", "tt-42")
	if err != nil || !slices.Equal(found, []MediaID{media.ID}) {
		t.Fatalf("MediaIDsByExternalID = %#v, %v", found, err)
	}
	batch, err := st.MediaExternalIDsBatch(ctx, []MediaID{media.ID})
	if err != nil || len(batch[media.ID]) != 1 {
		t.Fatalf("MediaExternalIDsBatch = %#v, %v", batch, err)
	}

	if err := st.UpsertArtworkCandidate(ctx, media.ID, "poster", "https://art.example/p.jpg", "plugin", "scraper"); err != nil {
		t.Fatalf("UpsertArtworkCandidate: %v", err)
	}
	candidateID := ArtworkCandidateID(media.ID, "poster", "https://art.example/p.jpg")
	all := LibraryFilter{AllLibraries: true}
	candidates, err := st.ArtworkCandidates(ctx, media.ID, "poster", all)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("ArtworkCandidates = %#v, %v", candidates, err)
	}
	if _, err := st.ArtworkCandidate(ctx, candidateID, all); err != nil {
		t.Fatalf("ArtworkCandidate: %v", err)
	}
	if err := st.MarkArtworkCandidateApplied(ctx, candidateID); err != nil {
		t.Fatalf("MarkArtworkCandidateApplied: %v", err)
	}
	allIDs, err := st.AllArtworkCandidateIDs(ctx)
	if err != nil || !slices.Contains(allIDs, candidateID) {
		t.Fatalf("AllArtworkCandidateIDs = %#v, %v", allIDs, err)
	}
	if err := st.ApplyArtworkCandidate(ctx, ArtworkCandidateApply{
		MediaID:      media.ID,
		Slot:         "backdrop",
		SourceURL:    "upload://backdrop",
		ApplyArtwork: func() (func() error, error) { return func() error { return nil }, nil },
	}); err != nil {
		t.Fatalf("ApplyArtworkCandidate: %v", err)
	}
	if manual, err := st.ArtworkSlotManual(ctx, media.ID, "backdrop"); err != nil || !manual {
		t.Fatalf("ArtworkSlotManual = %v, %v", manual, err)
	}

	queuedAt := time.Now().UTC().Truncate(time.Millisecond)
	if err := st.QueueUnmatched(ctx, UnmatchedItem{
		MediaID: media.ID, PluginID: "plugin", ScraperID: "scraper",
		Confidence: 0.4, Threshold: 0.8, QueuedAt: queuedAt,
	}); err != nil {
		t.Fatalf("QueueUnmatched: %v", err)
	}
	if queued, err := st.IsUnmatched(ctx, media.ID); err != nil || !queued {
		t.Fatalf("IsUnmatched = %v, %v", queued, err)
	}

	// A shared-catalog item queued earlier pages after the private one.
	var sharedID MediaID
	if err := st.catalogR.QueryRowContext(ctx,
		`SELECT id FROM media WHERE trashed_at_ms IS NULL ORDER BY id LIMIT 1`).Scan(&sharedID); err != nil {
		t.Fatalf("shared media: %v", err)
	}
	if err := st.QueueUnmatched(ctx, UnmatchedItem{
		MediaID: sharedID, PluginID: "plugin", ScraperID: "scraper",
		QueuedAt: queuedAt.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("QueueUnmatched shared: %v", err)
	}
	first, err := st.UnmatchedItems(ctx, all, 1, 0)
	if err != nil || len(first) != 1 || first[0].MediaID != media.ID || first[0].Title != "encrypted-identify" {
		t.Fatalf("UnmatchedItems page 1 = %#v, %v", first, err)
	}
	second, err := st.UnmatchedItems(ctx, all, 1, 1)
	if err != nil || len(second) != 1 || second[0].MediaID != sharedID {
		t.Fatalf("UnmatchedItems page 2 = %#v, %v", second, err)
	}

	for _, table := range []string{"media_external_id", "artwork_candidate", "unmatched_queue", "media_field_provenance"} {
		if n := identificationRowCount(t, st.catalogR, `SELECT COUNT(*) FROM `+table+` WHERE media_id = ?`, media.ID); n != 0 {
			t.Fatalf("shared %s holds %d rows for encrypted media", table, n)
		}
	}
	for _, table := range []string{"media_external_id", "artwork_candidate", "unmatched_queue"} {
		if n := identificationRowCount(t, private, `SELECT COUNT(*) FROM `+table+` WHERE media_id = ?`, media.ID); n == 0 {
			t.Fatalf("private %s holds no rows", table)
		}
	}
	if n := identificationRowCount(t, st.eventsR, `SELECT COUNT(*) FROM event_outbox WHERE aggregate_id = ?`, media.ID); n != 0 {
		t.Fatalf("event outbox carries %d rows for encrypted media", n)
	}
	if n := identificationRowCount(t, st.eventsR,
		`SELECT COUNT(*) FROM activity_entry WHERE operation_id LIKE 'artwork-apply-%' AND target_id <> ''`); n != 0 {
		t.Fatalf("artwork activity names encrypted media in %d rows", n)
	}
	if n := identificationRowCount(t, st.eventsR,
		`SELECT COUNT(*) FROM activity_entry WHERE operation_id LIKE 'artwork-apply-%'`); n != 1 {
		t.Fatalf("artwork activity rows = %d, want 1", n)
	}

	if cleared, err := st.ClearUnmatched(ctx, media.ID); err != nil || !cleared {
		t.Fatalf("ClearUnmatched = %v, %v", cleared, err)
	}
	if err := st.QueueUnmatched(ctx, UnmatchedItem{MediaID: media.ID, PluginID: "plugin", ScraperID: "scraper"}); err != nil {
		t.Fatalf("requeue: %v", err)
	}

	if err := st.SealEncryptedLibraryDB(ctx, media.LibraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if err := st.AddMediaExternalID(ctx, media.ID, "example-db", "tt-43"); err == nil {
		t.Fatal("sealed media accepted an external ID")
	}
	if err := st.UpsertArtworkCandidate(ctx, media.ID, "poster", "https://art.example/q.jpg", "", ""); err == nil {
		t.Fatal("sealed media accepted an artwork candidate")
	}
	if err := st.QueueUnmatched(ctx, UnmatchedItem{MediaID: media.ID, PluginID: "p", ScraperID: "s"}); err == nil {
		t.Fatal("sealed media accepted an Unmatched row")
	}
	if ids, err := st.MediaExternalIDs(ctx, media.ID); err != nil || len(ids) != 0 {
		t.Fatalf("sealed MediaExternalIDs = %#v, %v", ids, err)
	}
	if found, err := st.MediaIDsByExternalID(ctx, "example-db", "tt-42"); err != nil || len(found) != 0 {
		t.Fatalf("sealed MediaIDsByExternalID = %#v, %v", found, err)
	}
	if queued, err := st.IsUnmatched(ctx, media.ID); err != nil || queued {
		t.Fatalf("sealed IsUnmatched = %v, %v", queued, err)
	}
	if _, err := st.ArtworkCandidate(ctx, candidateID, all); err == nil {
		t.Fatal("sealed candidate stayed readable")
	}

	if err := st.UnsealEncryptedLibraryDB(ctx, media.LibraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("Unseal: %v", err)
	}
	if ids, err := st.MediaExternalIDs(ctx, media.ID); err != nil || len(ids) != 1 {
		t.Fatalf("reopened MediaExternalIDs = %#v, %v", ids, err)
	}
	if queued, err := st.IsUnmatched(ctx, media.ID); err != nil || !queued {
		t.Fatalf("reopened IsUnmatched = %v, %v", queued, err)
	}
}

// Artwork rows an earlier build wrote to the shared catalog for an Encrypted
// Library's Media move onto its plane at unseal, and a crash that left both
// copies converges on the next unseal.
func TestEncryptedUnsealEvacuatesStrandedArtworkState(t *testing.T) {
	ctx := context.Background()
	st, media := seededEncryptedMedia(t, "encrypted-stranded")
	defer st.Close()
	if err := st.SealEncryptedLibraryDB(ctx, media.LibraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	lockField := ArtworkLockField("poster")
	strand := func() {
		t.Helper()
		for _, row := range []struct {
			media MediaID
			url   string
		}{{media.ID, "https://art.example/stranded.jpg"}, {"media-elsewhere", "https://art.example/other.jpg"}} {
			if _, err := st.catalogW.ExecContext(ctx, `
				INSERT OR IGNORE INTO artwork_candidate(id, media_id, slot, source_url, plugin_id, scraper_id, applied, created_at_ms)
				VALUES (?, ?, 'poster', ?, '', '', 1, 1)`,
				ArtworkCandidateID(row.media, "poster", row.url), row.media, row.url); err != nil {
				t.Fatalf("strand candidate: %v", err)
			}
		}
		if _, err := st.catalogW.ExecContext(ctx, `
			INSERT OR IGNORE INTO media_field_provenance(media_id, field, source) VALUES (?, ?, 'manual')`,
			media.ID, lockField); err != nil {
			t.Fatalf("strand lock: %v", err)
		}
	}
	strand()
	if err := st.UnsealEncryptedLibraryDB(ctx, media.LibraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("Unseal: %v", err)
	}
	assertEvacuated := func() {
		t.Helper()
		private := st.encryptedDBs[media.LibraryID].reader
		if n := identificationRowCount(t, st.catalogR, `SELECT COUNT(*) FROM artwork_candidate WHERE media_id = ?`, media.ID); n != 0 {
			t.Fatalf("shared candidates left = %d", n)
		}
		if n := identificationRowCount(t, st.catalogR, `SELECT COUNT(*) FROM media_field_provenance WHERE media_id = ?`, media.ID); n != 0 {
			t.Fatalf("shared lock rows left = %d", n)
		}
		if n := identificationRowCount(t, st.catalogR, `SELECT COUNT(*) FROM artwork_candidate WHERE media_id = 'media-elsewhere'`); n != 1 {
			t.Fatalf("unrelated stranded candidate rows = %d, want 1", n)
		}
		if n := identificationRowCount(t, private, `SELECT COUNT(*) FROM artwork_candidate WHERE media_id = ? AND applied = 1`, media.ID); n != 1 {
			t.Fatalf("private applied candidates = %d, want 1", n)
		}
		if manual, err := st.ArtworkSlotManual(ctx, media.ID, "poster"); err != nil || !manual {
			t.Fatalf("ArtworkSlotManual = %v, %v", manual, err)
		}
	}
	assertEvacuated()

	// Crash after the private commit, before the shared delete: both copies.
	if err := st.SealEncryptedLibraryDB(ctx, media.LibraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	strand()
	if err := st.UnsealEncryptedLibraryDB(ctx, media.LibraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("Unseal: %v", err)
	}
	assertEvacuated()
}

func identificationRowCount(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}
