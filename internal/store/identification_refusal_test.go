package store

import (
	"errors"
	"sync"
	"testing"
)

// TestEncryptedIdentificationCommitsOnPrivatePlane: identification of an
// Encrypted Library's Media commits title, provenance, ordinal and revision on
// the Library's plane and leaves no target ID, title or candidate ID in the
// shared catalog or the shared event outbox; a sealed plane refuses the write.
func TestEncryptedIdentificationCommitsOnPrivatePlane(t *testing.T) {
	ctx := t.Context()
	st, media := seededEncryptedMedia(t, "identify-private")
	defer func() { _ = st.Close() }()
	title := "Private Identified Title 5b7e"
	actor := ProfileID("profile-owner")
	receipt, err := st.CommitMediaIdentification(ctx, media.ID, IdentificationCommit{
		RequestID:  "request-5b7e",
		Metadata:   MediaMetadata{Title: &title, OperatorOrdinal: &MediaOrdinal{Sequence: "season", Index: 3}, OrdinalActor: actor},
		Provenance: MediaMetadataProvenance{PluginID: "fixture", ScraperID: "fixture-scraper", CandidateID: "candidate-5b7e"},
	})
	if err != nil || receipt.Revision != 1 || receipt.Replayed {
		t.Fatalf("CommitMediaIdentification = %+v, %v", receipt, err)
	}
	plane := st.encryptedDBs[media.LibraryID]
	var private int
	if err := plane.reader.QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM media WHERE id = ? AND title = ?)
		     + (SELECT COUNT(*) FROM media_metadata_provenance WHERE media_id = ? AND candidate_id = 'candidate-5b7e')
		     + (SELECT COUNT(*) FROM media_operator_ordinal WHERE media_id = ? AND ordinal_index = 3)
		     + (SELECT COUNT(*) FROM media_identification_revision WHERE media_id = ? AND request_id = 'request-5b7e')
	`, media.ID, title, media.ID, media.ID, media.ID).Scan(&private); err != nil || private != 4 {
		t.Fatalf("private plane identification rows = %d, %v", private, err)
	}
	if got, ok, err := st.MediaMetadataProvenance(ctx, media.ID); err != nil || !ok || got.CandidateID != "candidate-5b7e" {
		t.Fatalf("MediaMetadataProvenance = %+v, %v, %v", got, ok, err)
	}
	if got, err := st.MediaOperatorOrdinal(ctx, media.ID); err != nil || got == nil || got.Index != 3 {
		t.Fatalf("MediaOperatorOrdinal = %+v, %v", got, err)
	}
	assertNoSharedIdentificationLeak(t, st, media.ID, title)

	if err := st.SealEncryptedLibraryDB(ctx, media.LibraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("SealEncryptedLibraryDB: %v", err)
	}
	sealedTitle := "Sealed Title 5b7e"
	if err := st.ApplyMediaIdentification(ctx, media.ID, MediaMetadata{Title: &sealedTitle},
		MediaMetadataProvenance{PluginID: "fixture", ScraperID: "fixture-scraper", CandidateID: "candidate-sealed"}, nil); err == nil {
		t.Fatal("identification of a sealed plane's Media reported success")
	}
	assertNoSharedIdentificationLeak(t, st, media.ID, sealedTitle)
}

func assertNoSharedIdentificationLeak(t *testing.T, st *Store, id MediaID, title string) {
	t.Helper()
	ctx := t.Context()
	var leaked int
	if err := st.catalogR.QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM media_metadata_provenance WHERE media_id = ? OR candidate_id LIKE 'candidate-%')
		     + (SELECT COUNT(*) FROM media WHERE id = ? OR title = ?)
		     + (SELECT COUNT(*) FROM media_operator_ordinal WHERE media_id = ?)
		     + (SELECT COUNT(*) FROM media_identification_revision WHERE media_id = ?)
	`, id, id, title, id, id).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("shared catalog identification rows = %d, %v", leaked, err)
	}
	if err := st.eventsR.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM event_outbox WHERE aggregate_id = ? OR payload_json LIKE '%' || ? || '%'`, id, title).
		Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("shared outbox rows naming the private Media = %d, %v", leaked, err)
	}
}

// TestIdentificationRequestReplayConvergesOnce: the same request ID with the
// same content commits one revision and replays report it; the same ID with
// different content is refused and leaves the first commit standing; revisions
// are immutable; concurrent replays of one request commit exactly once.
func TestIdentificationRequestReplayConvergesOnce(t *testing.T) {
	ctx := t.Context()
	for _, encrypted := range []bool{false, true} {
		var st *Store
		var media Media
		if encrypted {
			st, media = seededEncryptedMedia(t, "identify-replay")
		} else {
			st, media = seededSharedMedia(t)
		}
		title := "Replay Title"
		commit := IdentificationCommit{
			RequestID:  "request-replay",
			Metadata:   MediaMetadata{Title: &title},
			Provenance: MediaMetadataProvenance{PluginID: "fixture", ScraperID: "scraper", CandidateID: "c-1"},
		}
		var wg sync.WaitGroup
		receipts := make([]IdentificationReceipt, 6)
		errs := make([]error, len(receipts))
		for i := range receipts {
			wg.Add(1)
			go func() {
				defer wg.Done()
				receipts[i], errs[i] = st.CommitMediaIdentification(ctx, media.ID, commit)
			}()
		}
		wg.Wait()
		fresh := 0
		for i, err := range errs {
			if err != nil || receipts[i].Revision != 1 {
				t.Fatalf("encrypted=%v commit %d = %+v, %v", encrypted, i, receipts[i], err)
			}
			if !receipts[i].Replayed {
				fresh++
			}
		}
		if fresh != 1 {
			t.Fatalf("encrypted=%v fresh commits = %d, want exactly 1", encrypted, fresh)
		}
		other := "Different Title"
		reused := commit
		reused.Metadata = MediaMetadata{Title: &other}
		if _, err := st.CommitMediaIdentification(ctx, media.ID, reused); !errors.Is(err, ErrIdentificationRequestConflict) {
			t.Fatalf("encrypted=%v reused request ID err = %v", encrypted, err)
		}
		got, err := st.Media(ctx, media.ID, LibraryFilter{AllLibraries: true})
		if err != nil || got.Title != title {
			t.Fatalf("encrypted=%v title after refused reuse = %q, %v", encrypted, got.Title, err)
		}
		next, err := st.CommitMediaIdentification(ctx, media.ID, IdentificationCommit{
			Metadata: MediaMetadata{Title: &other}, Provenance: commit.Provenance,
		})
		if err != nil || next.Revision != 2 || next.Replayed {
			t.Fatalf("encrypted=%v second identification = %+v, %v", encrypted, next, err)
		}
		revisions, err := st.MediaIdentificationRevisions(ctx, media.ID)
		if err != nil || len(revisions) != 2 || revisions[0].RequestID != "request-replay" {
			t.Fatalf("encrypted=%v revisions = %+v, %v", encrypted, revisions, err)
		}
		plane, err := st.catalogPlaneForMediaLocked(ctx, media.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := plane.writer.ExecContext(ctx,
			`UPDATE media_identification_revision SET candidate_id = 'rewritten' WHERE media_id = ?`, media.ID); err == nil {
			t.Fatalf("encrypted=%v identification revision accepted an update", encrypted)
		}
		if _, err := plane.writer.ExecContext(ctx,
			`UPDATE media_identification_revision SET actor_profile_id = 'profile-other' WHERE media_id = ?`, media.ID); err == nil {
			t.Fatalf("encrypted=%v identification revision accepted non-null actor rewrite", encrypted)
		}
		if _, err := plane.writer.ExecContext(ctx,
			`UPDATE media_identification_revision SET actor_profile_id = NULL, candidate_id = 'rewritten' WHERE media_id = ?`, media.ID); err == nil {
			t.Fatalf("encrypted=%v identification revision accepted multi-column update with actor null", encrypted)
		}
		if _, err := plane.writer.ExecContext(ctx,
			`UPDATE media_identification_revision SET actor_profile_id = NULL WHERE media_id = ?`, media.ID); err != nil {
			t.Fatalf("encrypted=%v identification revision refused nulling actor_profile_id: %v", encrypted, err)
		}
		_ = st.Close()
	}
}

func seededSharedMedia(t *testing.T) (*Store, Media) {
	t.Helper()
	st, err := Open(t.Context(), migratedDataDir(t), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	media, _, err := st.ImportMediaFile(t.Context(), encryptedImport("library-local", "sha256:shared-identify", "/shared/identify.mkv"))
	if err != nil {
		_ = st.Close()
		t.Fatalf("ImportMediaFile: %v", err)
	}
	return st, media
}
