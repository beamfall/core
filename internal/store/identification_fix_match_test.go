package store

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
)

// TestFixMatchCommitsAuditOnceOnOwnPlane: a Fix Match commits the metadata,
// the match pin, the Unmatched-queue resolution and one immutable fix-match
// revision together on the Media's own plane; concurrent replays of the same
// request commit exactly one audit row; an encrypted Media's audit and pin stay
// on its private plane and nothing names it in the shared catalog or outbox.
func TestFixMatchCommitsAuditOnceOnOwnPlane(t *testing.T) {
	ctx := t.Context()
	for _, encrypted := range []bool{false, true} {
		var st *Store
		var media Media
		if encrypted {
			st, media = seededEncryptedMedia(t, "fix-match-private")
		} else {
			st, media = seededSharedMedia(t)
		}
		if err := st.QueueUnmatched(ctx, UnmatchedItem{MediaID: media.ID, PluginID: "fixture", ScraperID: "scraper", Confidence: 0.2, Threshold: 0.8}); err != nil {
			t.Fatalf("encrypted=%v QueueUnmatched: %v", encrypted, err)
		}
		title := "Fixed Match Title 91c4"
		commit := FixMatchCommit{
			IdentificationCommit: IdentificationCommit{
				RequestID:  "fix-request-91c4",
				Metadata:   MediaMetadata{Title: &title, ClearLocks: []string{"title"}, ClearOrdinal: true},
				Provenance: MediaMetadataProvenance{PluginID: "fixture", ScraperID: "scraper", CandidateID: "candidate-91c4"},
			},
			Filter: LibraryFilter{AllLibraries: true},
		}
		var wg sync.WaitGroup
		receipts := make([]IdentificationReceipt, 5)
		errs := make([]error, len(receipts))
		for i := range receipts {
			wg.Add(1)
			go func() {
				defer wg.Done()
				receipts[i], errs[i] = st.CommitFixMatch(ctx, media.ID, commit)
			}()
		}
		wg.Wait()
		fresh := 0
		for i, err := range errs {
			if err != nil || receipts[i].Revision != 1 {
				t.Fatalf("encrypted=%v CommitFixMatch %d = %+v, %v", encrypted, i, receipts[i], err)
			}
			if !receipts[i].Replayed {
				fresh++
			}
		}
		if fresh != 1 {
			t.Fatalf("encrypted=%v fresh fix-match commits = %d, want 1", encrypted, fresh)
		}
		revisions, err := st.MediaIdentificationRevisions(ctx, media.ID)
		if err != nil || len(revisions) != 1 || revisions[0].Kind != IdentificationKindFixMatch {
			t.Fatalf("encrypted=%v revisions = %+v, %v", encrypted, revisions, err)
		}
		var audit FixMatchAudit
		if err := json.Unmarshal([]byte(revisions[0].DetailJSON), &audit); err != nil {
			t.Fatalf("encrypted=%v audit detail %q: %v", encrypted, revisions[0].DetailJSON, err)
		}
		if audit.Ordinal != "cleared" || audit.PinnedFiles != 1 || !audit.UnmatchedCleared || len(audit.ClearedLocks) != 1 {
			t.Fatalf("encrypted=%v audit = %+v", encrypted, audit)
		}
		if queued, err := st.IsUnmatched(ctx, media.ID); err != nil || queued {
			t.Fatalf("encrypted=%v IsUnmatched after fix = %v, %v", encrypted, queued, err)
		}
		plane, err := st.catalogPlaneForMediaLocked(ctx, media.ID)
		if err != nil {
			t.Fatal(err)
		}
		var pinned int
		if err := plane.reader.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM media_file WHERE media_id = ? AND match_pinned = 1`, media.ID).Scan(&pinned); err != nil || pinned != 1 {
			t.Fatalf("encrypted=%v pinned files = %d, %v", encrypted, pinned, err)
		}
		// A different request kind with the same ID is a different request.
		if _, err := st.CommitMediaIdentification(ctx, media.ID, commit.IdentificationCommit); !errors.Is(err, ErrIdentificationRequestConflict) {
			t.Fatalf("encrypted=%v identify reusing a fix-match request ID err = %v", encrypted, err)
		}
		if encrypted {
			assertNoSharedIdentificationLeak(t, st, media.ID, title)
		}
		_ = st.Close()
	}
}

// TestFixMatchRefusalCommitsNothing: a Fix Match invisible to the caller's
// filter, or failing validation, leaves metadata, pin, queue and audit as they
// were; the transaction is all-or-nothing.
func TestFixMatchRefusalCommitsNothing(t *testing.T) {
	ctx := t.Context()
	st, media := seededSharedMedia(t)
	defer func() { _ = st.Close() }()
	if err := st.QueueUnmatched(ctx, UnmatchedItem{MediaID: media.ID, PluginID: "fixture", ScraperID: "scraper"}); err != nil {
		t.Fatalf("QueueUnmatched: %v", err)
	}
	title := "Refused Fix Title"
	commit := FixMatchCommit{
		IdentificationCommit: IdentificationCommit{
			Metadata:   MediaMetadata{Title: &title},
			Provenance: MediaMetadataProvenance{PluginID: "fixture", ScraperID: "scraper", CandidateID: "c-refused"},
		},
		Filter: LibraryFilter{AllowedIDs: []LibraryID{"library-other"}},
	}
	if _, err := st.CommitFixMatch(ctx, media.ID, commit); err == nil {
		t.Fatal("Fix Match outside the caller's libraries reported success")
	}
	commit.Filter = LibraryFilter{AllLibraries: true}
	commit.Metadata.ClearLocks = []string{"no-such-field"}
	if _, err := st.CommitFixMatch(ctx, media.ID, commit); err == nil {
		t.Fatal("Fix Match clearing an unknown lock reported success")
	}
	got, err := st.Media(ctx, media.ID, LibraryFilter{AllLibraries: true})
	if err != nil || got.Title == title {
		t.Fatalf("title after refused Fix Match = %q, %v", got.Title, err)
	}
	if queued, err := st.IsUnmatched(ctx, media.ID); err != nil || !queued {
		t.Fatalf("IsUnmatched after refused Fix Match = %v, %v", queued, err)
	}
	var pinned, revisions int
	if err := st.catalogR.QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM media_file WHERE media_id = ? AND match_pinned = 1),
		       (SELECT COUNT(*) FROM media_identification_revision WHERE media_id = ?)
	`, media.ID, media.ID).Scan(&pinned, &revisions); err != nil || pinned != 0 || revisions != 0 {
		t.Fatalf("pinned=%d revisions=%d after refused Fix Match, %v", pinned, revisions, err)
	}
}
