package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestPurgeTrashedBeforePrunesExternalIDsAndEngagementOrphans(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	revealed := LibraryFilter{AllowedIDs: []LibraryID{"library-local", "library-archive"}, Revealed: true}

	if err := st.AddMediaExternalID(ctx, "media-alpha", "fixture", "purged"); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMediaExternalID(ctx, "media-archive", "fixture", "survivor"); err != nil {
		t.Fatal(err)
	}
	if err := st.QueueAdd(ctx, "profile-default", "media-alpha", nil, revealed); err != nil {
		t.Fatal(err)
	}
	if err := st.QueueAdd(ctx, "profile-default", "media-archive", nil, revealed); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMediaFavorite(ctx, "profile-default", "media-alpha", true, revealed); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMediaFavorite(ctx, "profile-default", "media-archive", true, revealed); err != nil {
		t.Fatal(err)
	}
	insertRawQueueRow(t, st, "profile-default", 20, "media-old-orphan")
	insertRawFavoriteRow(t, st, "profile-default", "media-old-orphan")

	if err := st.TrashMedia(ctx, "media-alpha", revealed); err != nil {
		t.Fatal(err)
	}
	purged, err := st.PurgeTrashedBefore(ctx, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if purged != 1 {
		t.Fatalf("purged = %d, want 1", purged)
	}

	if got := countCatalogRows(t, st, `SELECT COUNT(*) FROM media_external_id WHERE media_id = 'media-alpha'`); got != 0 {
		t.Fatalf("purged media external ids = %d, want 0", got)
	}
	if got := countCatalogRows(t, st, `SELECT COUNT(*) FROM media_external_id WHERE media_id = 'media-archive'`); got != 1 {
		t.Fatalf("surviving media external ids = %d, want 1", got)
	}
	for _, mediaID := range []MediaID{"media-alpha", "media-old-orphan"} {
		if got := countEventRows(t, st, "play_queue", mediaID); got != 0 {
			t.Fatalf("play_queue rows for %s = %d, want 0", mediaID, got)
		}
		if got := countEventRows(t, st, "media_favorite", mediaID); got != 0 {
			t.Fatalf("media_favorite rows for %s = %d, want 0", mediaID, got)
		}
	}
	if got := countEventRows(t, st, "play_queue", "media-archive"); got != 1 {
		t.Fatalf("hidden-but-alive queue rows = %d, want 1", got)
	}
	if got := countEventRows(t, st, "media_favorite", "media-archive"); got != 1 {
		t.Fatalf("hidden-but-alive favorite rows = %d, want 1", got)
	}
}

func TestPurgeTrashedBeforeReportsPruneFailureAndRetryRecovers(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	insertRawProgressRow(t, st, "profile-adult", "media-alpha")
	if err := st.TrashMedia(ctx, "media-alpha", LibraryFilter{Revealed: true, AllLibraries: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.eventsW.ExecContext(ctx, `
		CREATE TRIGGER fail_purge_engagement_prune
		BEFORE DELETE ON playback_progress
		WHEN OLD.media_id = 'media-alpha'
		BEGIN
			SELECT RAISE(ABORT, 'injected engagement prune failure');
		END
	`); err != nil {
		t.Fatal(err)
	}

	purged, err := st.PurgeTrashedBefore(ctx, time.Now().UTC().Add(time.Hour))
	if err == nil || !strings.Contains(err.Error(), "injected engagement prune failure") {
		t.Fatalf("first purge err = %v, want injected prune failure", err)
	}
	if purged != 1 {
		t.Fatalf("first purge affected = %d, want committed catalog deletion count 1", purged)
	}
	if got := countEventRows(t, st, "playback_progress", "media-alpha"); got != 1 {
		t.Fatalf("orphan rows after failed prune = %d, want 1 pending retry", got)
	}
	if _, err := st.eventsW.ExecContext(ctx, `DROP TRIGGER fail_purge_engagement_prune`); err != nil {
		t.Fatal(err)
	}

	purged, err = st.PurgeTrashedBefore(ctx, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("retry purge: %v", err)
	}
	if purged != 0 {
		t.Fatalf("retry catalog affected = %d, want 0", purged)
	}
	if got := countEventRows(t, st, "playback_progress", "media-alpha"); got != 0 {
		t.Fatalf("orphan rows after retry = %d, want 0", got)
	}
}

func TestPruneOrphanedEngagementRowsUsesExistenceNotVisibility(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	revealed := LibraryFilter{AllowedIDs: []LibraryID{"library-local", "library-archive"}, Revealed: true}

	if err := st.QueueAdd(ctx, "profile-default", "media-alpha", nil, revealed); err != nil {
		t.Fatal(err)
	}
	if err := st.QueueAdd(ctx, "profile-default", "media-archive", nil, revealed); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMediaFavorite(ctx, "profile-default", "media-alpha", true, revealed); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMediaFavorite(ctx, "profile-default", "media-archive", true, revealed); err != nil {
		t.Fatal(err)
	}
	insertRawQueueRow(t, st, "profile-default", 10, "media-missing")
	insertRawFavoriteRow(t, st, "profile-default", "media-missing")

	counts, err := st.PruneOrphanedEngagementRows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts.QueueRows != 1 || counts.FavoriteRows != 1 {
		t.Fatalf("counts = %#v, want one queue and one favorite row", counts)
	}
	for _, mediaID := range []MediaID{"media-alpha", "media-archive"} {
		if got := countEventRows(t, st, "play_queue", mediaID); got != 1 {
			t.Fatalf("play_queue rows for live %s = %d, want 1", mediaID, got)
		}
		if got := countEventRows(t, st, "media_favorite", mediaID); got != 1 {
			t.Fatalf("media_favorite rows for live %s = %d, want 1", mediaID, got)
		}
	}
	if got := countEventRows(t, st, "play_queue", "media-missing"); got != 0 {
		t.Fatalf("orphaned queue rows = %d, want 0", got)
	}
	if got := countEventRows(t, st, "media_favorite", "media-missing"); got != 0 {
		t.Fatalf("orphaned favorite rows = %d, want 0", got)
	}

	counts, err = st.PruneOrphanedEngagementRows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts.QueueRows != 0 || counts.FavoriteRows != 0 {
		t.Fatalf("second prune counts = %#v, want zero rows", counts)
	}
}

// TestPurgeTrashedBeforeErasesProgressAndEmbedding is the regression for the
// durable-delete contract: purging trashed Media must also erase its
// per-Profile playback_progress and its fingerprint-keyed media_embedding, so a
// later re-import of the same content (which deterministically regenerates the
// same Media id) does not resurrect watch history or a semantic-search vector of
// the deleted — possibly restricted — item.
func TestPurgeTrashedBeforeErasesProgressAndEmbedding(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	revealed := LibraryFilter{AllowedIDs: []LibraryID{"library-local", "library-archive"}, Revealed: true}

	const alphaFP = "sha256:alpha-fingerprint"
	const archiveFP = "sha256:archive-fingerprint"
	insertRawMediaFile(t, st, "media-alpha", alphaFP)
	insertRawMediaFile(t, st, "media-archive", archiveFP)

	// Watch state for the soon-purged item, the survivor, and a pre-existing orphan.
	insertRawProgressRow(t, st, "profile-default", "media-alpha")
	insertRawProgressRow(t, st, "profile-default", "media-archive")
	insertRawProgressRow(t, st, "profile-default", "media-old-orphan")

	// Semantic vectors keyed by content fingerprint.
	if err := st.UpsertEmbedding(ctx, alphaFP, EmbeddingKind("text"), []float32{0.1, 0.2, 0.3}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEmbedding(ctx, archiveFP, EmbeddingKind("text"), []float32{0.4, 0.5, 0.6}); err != nil {
		t.Fatal(err)
	}

	if err := st.TrashMedia(ctx, "media-alpha", revealed); err != nil {
		t.Fatal(err)
	}
	if _, err := st.PurgeTrashedBefore(ctx, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	// playback_progress: gone for the purged item and the pre-existing orphan,
	// kept for the still-alive survivor.
	for _, mediaID := range []MediaID{"media-alpha", "media-old-orphan"} {
		if got := countEventRows(t, st, "playback_progress", mediaID); got != 0 {
			t.Fatalf("playback_progress for %s = %d, want 0 (resurrection risk)", mediaID, got)
		}
	}
	if got := countEventRows(t, st, "playback_progress", "media-archive"); got != 1 {
		t.Fatalf("survivor playback_progress = %d, want 1", got)
	}

	// media_embedding: gone for the purged fingerprint, kept for the survivor.
	if got := countCatalogRows(t, st, `SELECT COUNT(*) FROM media_embedding WHERE media_fingerprint = '`+alphaFP+`'`); got != 0 {
		t.Fatalf("purged media_embedding = %d, want 0 (search-ghost risk)", got)
	}
	if got := countCatalogRows(t, st, `SELECT COUNT(*) FROM media_embedding WHERE media_fingerprint = '`+archiveFP+`'`); got != 1 {
		t.Fatalf("survivor media_embedding = %d, want 1", got)
	}
}

func insertRawQueueRow(t *testing.T, st *Store, profileID ProfileID, position int, mediaID MediaID) {
	t.Helper()
	_, err := st.eventsW.ExecContext(context.Background(), `
		INSERT INTO play_queue(profile_id, position, media_id, added_at_ms)
		VALUES (?, ?, ?, ?)
	`, profileID, position, mediaID, timeToMillis(time.Now().UTC()))
	if err != nil {
		t.Fatal(err)
	}
}

func insertRawFavoriteRow(t *testing.T, st *Store, profileID ProfileID, mediaID MediaID) {
	t.Helper()
	_, err := st.eventsW.ExecContext(context.Background(), `
		INSERT INTO media_favorite(profile_id, media_id, favorited_at_ms)
		VALUES (?, ?, ?)
	`, profileID, mediaID, timeToMillis(time.Now().UTC()))
	if err != nil {
		t.Fatal(err)
	}
}

func insertRawProgressRow(t *testing.T, st *Store, profileID ProfileID, mediaID MediaID) {
	t.Helper()
	_, err := st.eventsW.ExecContext(context.Background(), `
		INSERT INTO playback_progress(profile_id, media_id, position_seconds, duration_seconds, watched, updated_at_ms)
		VALUES (?, ?, ?, ?, ?, ?)
	`, profileID, mediaID, 30, 120, 0, timeToMillis(time.Now().UTC()))
	if err != nil {
		t.Fatal(err)
	}
}

func insertRawMediaFile(t *testing.T, st *Store, mediaID MediaID, fingerprint string) {
	t.Helper()
	_, err := st.catalogW.ExecContext(context.Background(), `
		INSERT INTO media_file(id, content_fingerprint, media_id, path, last_seen_at_ms)
		VALUES (?, ?, ?, ?, ?)
	`, fingerprint, fingerprint, mediaID, "/fixtures/"+string(mediaID)+".mkv", timeToMillis(time.Now().UTC()))
	if err != nil {
		t.Fatal(err)
	}
}

func countEventRows(t *testing.T, st *Store, table string, mediaID MediaID) int {
	t.Helper()
	var count int
	if err := st.eventsR.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM `+table+` WHERE media_id = ?`, mediaID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func countCatalogRows(t *testing.T, st *Store, query string) int {
	t.Helper()
	var count int
	if err := st.catalogR.QueryRowContext(context.Background(), query).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
