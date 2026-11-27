package store

import (
	"sync"
	"testing"
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
		// CORE-3516 #10: an artwork-free identification supersedes older pending
		// artwork actions rather than leaving them active across a restart.
		if _, err := st.CommitMediaIdentification(ctx, media.ID, IdentificationCommit{
			Metadata:   MediaMetadata{Title: &next},
			Provenance: MediaMetadataProvenance{PluginID: "fixture", ScraperID: "scraper", CandidateID: "c-no-art"},
		}); err != nil {
			t.Fatalf("encrypted=%v artwork-free commit: %v", encrypted, err)
		}
		if afterClear, err := st.PendingArtworkActionsForMedia(ctx, media.ID); err != nil || len(afterClear) != 0 {
			t.Fatalf("encrypted=%v pending after artwork-free replacement = %+v, %v", encrypted, afterClear, err)
		}
		_ = st.Close()
	}
}
