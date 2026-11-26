package store

import (
	"sync"
	"testing"
	"time"
)

// TestArtworkActionsCommitWithRevisionAndCompleteOnce: an identification's
// artwork is recorded as pending actions in its own transaction and on its
// own plane; concurrent completions of one action record exactly one; a
// newer identification supersedes the older pending actions; a replayed
// request enqueues nothing; an encrypted Media's actions never reach the
// shared catalog.
func TestArtworkActionsCommitWithRevisionAndCompleteOnce(t *testing.T) {
	ctx := t.Context()
	for _, encrypted := range []bool{false, true} {
		var st *Store
		var media Media
		if encrypted {
			st, media = seededEncryptedMedia(t, "artwork-action")
		} else {
			st, media = seededSharedMedia(t)
		}
		title := "Artwork Action Title"
		commit := IdentificationCommit{
			RequestID: "artwork-request-1",
			Metadata: MediaMetadata{Title: &title, Artwork: map[string]string{
				"poster": "https://img.example.test/private-poster.jpg", "backdrop": "https://img.example.test/b.jpg",
			}},
			Provenance: MediaMetadataProvenance{PluginID: "fixture", ScraperID: "scraper", CandidateID: "c-1"},
		}
		if _, err := st.CommitMediaIdentification(ctx, media.ID, commit); err != nil {
			t.Fatalf("encrypted=%v commit: %v", encrypted, err)
		}
		if receipt, err := st.CommitMediaIdentification(ctx, media.ID, commit); err != nil || !receipt.Replayed {
			t.Fatalf("encrypted=%v replay = %+v, %v", encrypted, receipt, err)
		}
		pending, err := st.PendingArtworkActionsForMedia(ctx, media.ID)
		if err != nil || len(pending) != 2 || pending[0].Slot != "backdrop" || pending[1].Revision != 1 {
			t.Fatalf("encrypted=%v pending = %+v, %v", encrypted, pending, err)
		}
		all, err := st.PendingArtworkActions(ctx, 10)
		if err != nil || len(all) != 2 {
			t.Fatalf("encrypted=%v PendingArtworkActions = %+v, %v", encrypted, all, err)
		}
		var wg sync.WaitGroup
		wins := make([]bool, 6)
		for i := range wins {
			wg.Add(1)
			go func() {
				defer wg.Done()
				done, err := st.CompleteArtworkAction(ctx, pending[0], "imported")
				if err != nil {
					t.Errorf("encrypted=%v CompleteArtworkAction: %v", encrypted, err)
				}
				wins[i] = done
			}()
		}
		wg.Wait()
		won := 0
		for _, w := range wins {
			if w {
				won++
			}
		}
		if won != 1 {
			t.Fatalf("encrypted=%v completions recorded = %d, want exactly 1", encrypted, won)
		}
		next := "Second Title"
		if _, err := st.CommitMediaIdentification(ctx, media.ID, IdentificationCommit{
			Metadata:   MediaMetadata{Title: &next, Artwork: map[string]string{"poster": "https://img.example.test/p2.jpg"}},
			Provenance: commit.Provenance,
		}); err != nil {
			t.Fatalf("encrypted=%v second commit: %v", encrypted, err)
		}
		pending, err = st.PendingArtworkActionsForMedia(ctx, media.ID)
		if err != nil || len(pending) != 1 || pending[0].Revision != 2 || pending[0].SourceURL != "https://img.example.test/p2.jpg" {
			t.Fatalf("encrypted=%v pending after newer revision = %+v, %v", encrypted, pending, err)
		}
		if done, err := st.CompleteArtworkAction(ctx, ArtworkAction{MediaID: media.ID, Revision: 1, Slot: "poster"}, "imported"); err != nil || done {
			t.Fatalf("encrypted=%v completing a superseded action = %v, %v", encrypted, done, err)
		}
		var shared int
		if err := st.catalogR.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM media_artwork_action WHERE media_id = ? OR source_url LIKE '%private-poster%'`, media.ID).
			Scan(&shared); err != nil {
			t.Fatal(err)
		}
		if encrypted && shared != 0 {
			t.Fatalf("shared catalog artwork actions for the private Media = %d", shared)
		}
		if !encrypted && shared != 3 {
			t.Fatalf("shared catalog artwork actions = %d, want 3", shared)
		}
		_ = st.Close()
	}
}

// TestArtworkActionRetryBackoffAndAttemptCap verifies that transient fetch
// failures keep the action pending until its backoff window expires and cap
// out cleanly in done once maxAttempts is reached on both shared and encrypted
// planes.
func TestArtworkActionRetryBackoffAndAttemptCap(t *testing.T) {
	ctx := t.Context()
	for _, encrypted := range []bool{false, true} {
		var st *Store
		var media Media
		if encrypted {
			st, media = seededEncryptedMedia(t, "artwork-retry-private")
		} else {
			st, media = seededSharedMedia(t)
		}
		now := time.Date(2026, 11, 26, 8, 0, 0, 0, time.UTC)
		st.now = func() time.Time { return now }
		title := "Retry Title"
		if _, err := st.CommitMediaIdentification(ctx, media.ID, IdentificationCommit{
			Metadata:   MediaMetadata{Title: &title, Artwork: map[string]string{"poster": "https://img.example.test/retry.jpg"}},
			Provenance: MediaMetadataProvenance{PluginID: "fixture", ScraperID: "scraper"},
		}); err != nil {
			t.Fatalf("encrypted=%v CommitMediaIdentification: %v", encrypted, err)
		}
		pending, err := st.PendingArtworkActions(ctx, 10)
		if err != nil || len(pending) != 1 || pending[0].Attempts != 0 {
			t.Fatalf("encrypted=%v initial pending = %+v, %v", encrypted, pending, err)
		}
		requeued, err := st.RecordArtworkActionRetry(ctx, pending[0], "fetch-failed", time.Minute, 3)
		if err != nil || !requeued {
			t.Fatalf("encrypted=%v RecordArtworkActionRetry #1 = %v, %v", encrypted, requeued, err)
		}
		if due, err := st.PendingArtworkActions(ctx, 10); err != nil || len(due) != 0 {
			t.Fatalf("encrypted=%v due before backoff = %+v, %v", encrypted, due, err)
		}
		if dueForMedia, err := st.PendingArtworkActionsForMedia(ctx, media.ID); err != nil || len(dueForMedia) != 0 {
			t.Fatalf("encrypted=%v PendingArtworkActionsForMedia before backoff = %+v, %v", encrypted, dueForMedia, err)
		}
		now = now.Add(time.Minute)
		dueForMedia, err := st.PendingArtworkActionsForMedia(ctx, media.ID)
		if err != nil || len(dueForMedia) != 1 || dueForMedia[0].Attempts != 1 {
			t.Fatalf("encrypted=%v PendingArtworkActionsForMedia after backoff = %+v, %v", encrypted, dueForMedia, err)
		}
		due, err := st.PendingArtworkActions(ctx, 10)
		if err != nil || len(due) != 1 || due[0].Attempts != 1 {
			t.Fatalf("encrypted=%v due after backoff = %+v, %v", encrypted, due, err)
		}
		requeued, err = st.RecordArtworkActionRetry(ctx, due[0], "fetch-failed", time.Minute, 3)
		if err != nil || !requeued {
			t.Fatalf("encrypted=%v RecordArtworkActionRetry #2 = %v, %v", encrypted, requeued, err)
		}
		now = now.Add(time.Minute)
		due, err = st.PendingArtworkActions(ctx, 10)
		if err != nil || len(due) != 1 || due[0].Attempts != 2 {
			t.Fatalf("encrypted=%v due #3 = %+v, %v", encrypted, due, err)
		}
		requeued, err = st.RecordArtworkActionRetry(ctx, due[0], "fetch-failed", time.Minute, 3)
		if err != nil || requeued {
			t.Fatalf("encrypted=%v RecordArtworkActionRetry #3 = %v, %v; want terminal done", encrypted, requeued, err)
		}
		if left, err := st.PendingArtworkActions(ctx, 10); err != nil || len(left) != 0 {
			t.Fatalf("encrypted=%v left pending after cap = %+v, %v", encrypted, left, err)
		}
		_ = st.Close()
	}
}
