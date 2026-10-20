package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// METAPARITY-6 (ADR-0068 associate-vs-create): a hand-authored Media carries the
// operator's typed identity, a selected file, and — definitionally — no external
// ID at all.

// TestCreateManualMediaMintsNoExternalID pins the defining absence: the created
// Media is a normal, browsable, file-backed row that Beamfall never claims to
// have identified against any provider.
func TestCreateManualMediaMintsNoExternalID(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	filter := LibraryFilter{AllLibraries: true, Revealed: true}

	mediaID, err := CreateManualMediaFixture(ctx, st)
	if err != nil {
		t.Fatalf("CreateManualMedia: %v", err)
	}
	media, err := st.Media(ctx, mediaID, filter)
	if err != nil {
		t.Fatalf("Media: %v", err)
	}
	if media.Title != "Home Movie 1994" || media.Year != 1994 {
		t.Fatalf("media = %#v, want the hand-entered title and year", media)
	}
	if media.MutatedBy != MutationSourceManual {
		t.Fatalf("mutatedBy = %q, want manual — a re-scan must not outrank the operator", media.MutatedBy)
	}
	ids, err := st.MediaExternalIDs(ctx, mediaID)
	if err != nil {
		t.Fatalf("MediaExternalIDs: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("external IDs = %#v, want none for a hand-authored Media", ids)
	}
	files, err := st.MediaFilesForMedia(ctx, mediaID, filter)
	if err != nil {
		t.Fatalf("MediaFilesForMedia: %v", err)
	}
	if len(files) != 1 || files[0].Path != "/media/home-movie.mkv" {
		t.Fatalf("files = %#v, want the operator-selected file attached", files)
	}
}

// TestCreateManualMediaIsIdempotent is the mockup's save-error recovery: a retry
// after a partial failure must land on the same Media, never a duplicate.
func TestCreateManualMediaIsIdempotent(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)

	first, err := CreateManualMediaFixture(ctx, st)
	if err != nil {
		t.Fatalf("first CreateManualMedia: %v", err)
	}
	second, err := CreateManualMediaFixture(ctx, st)
	if err != nil {
		t.Fatalf("retried CreateManualMedia: %v", err)
	}
	if first != second {
		t.Fatalf("retry forked the Media: %q vs %q", first, second)
	}
}

// TestCreateManualMediaRequiresTitleAndLibrary keeps the required-field contract
// at the store boundary, not only in the form.
func TestCreateManualMediaRequiresTitleAndLibrary(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	if _, err := st.CreateManualMedia(ctx, ManualMedia{LibraryID: "library-local"}); err == nil {
		t.Fatal("CreateManualMedia accepted an empty title")
	}
	if _, err := st.CreateManualMedia(ctx, ManualMedia{Title: "Untitled"}); err == nil {
		t.Fatal("CreateManualMedia accepted an empty library")
	}
	if _, err := st.CreateManualMedia(ctx, ManualMedia{LibraryID: "library-nope", Title: "Untitled"}); err == nil {
		t.Fatal("CreateManualMedia accepted an unknown library")
	}
}

func TestUnmatchedCreateReservationSurvivesRestartAndRejectsConflict(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	first, err := Open(ctx, dataDir, Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	recordID, err := first.QueueUnmatchedRecord(ctx, UnmatchedRecord{
		Origin:        UnmatchedOriginScan,
		LibraryID:     "library-local",
		SourceHandle:  "/media/reserved.mkv",
		FailureReason: UnmatchedReasonNoCandidates,
	})
	if err != nil {
		t.Fatal(err)
	}
	injected := errors.New("injected pre-finalization failure")
	var reservedMediaID MediaID
	if _, err := first.CreateManualMediaFromUnmatched(
		ctx,
		recordID,
		"library-local",
		"Reserved Media",
		2001,
		"digest-a",
		time.Unix(100, 0),
		func(mediaID MediaID) error {
			reservedMediaID = mediaID
			return injected
		},
	); !errors.Is(err, injected) {
		t.Fatalf("failed CreateManualMediaFromUnmatched = %v, want %v", err, injected)
	}
	record, err := first.UnmatchedRecordByID(ctx, recordID, LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		t.Fatal(err)
	}
	if record.Resolved() {
		t.Fatalf("reserved record resolved before finalization: %#v", record)
	}
	if record.MediaID != reservedMediaID || record.LibraryID != "library-local" {
		t.Fatalf("pending reservation = %#v, want visible Media %q", record, reservedMediaID)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(ctx, dataDir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if _, err := reopened.CreateManualMediaFromUnmatched(
		ctx, recordID, "library-local", "Reserved Media", 2001, "digest-b", time.Unix(200, 0),
	); !errors.Is(err, ErrUnmatchedCreateConflict) {
		t.Fatalf("conflicting post-restart create = %v, want %v", err, ErrUnmatchedCreateConflict)
	}
	stageCalls := 0
	var stagedMediaID MediaID
	resumedMediaID, err := reopened.CreateManualMediaFromUnmatched(
		ctx,
		recordID,
		"library-local",
		"Reserved Media",
		2001,
		"digest-a",
		time.Unix(200, 0),
		func(mediaID MediaID) error {
			stageCalls++
			stagedMediaID = mediaID
			return nil
		},
	)
	if err != nil {
		t.Fatalf("resume create: %v", err)
	}
	if resumedMediaID != reservedMediaID || stagedMediaID != reservedMediaID || stageCalls != 1 {
		t.Fatalf(
			"resumed Media=%q staged=%q calls=%d, want reserved %q once",
			resumedMediaID, stagedMediaID, stageCalls, reservedMediaID,
		)
	}
	replayedMediaID, err := reopened.CreateManualMediaFromUnmatched(
		ctx,
		recordID,
		"library-local",
		"Reserved Media",
		2001,
		"digest-a",
		time.Unix(300, 0),
		func(MediaID) error {
			stageCalls++
			return injected
		},
	)
	if err != nil || replayedMediaID != reservedMediaID || stageCalls != 1 {
		t.Fatalf(
			"finalized replay Media=%q calls=%d err=%v, want %q without stage",
			replayedMediaID, stageCalls, err, reservedMediaID,
		)
	}
	record, err = reopened.UnmatchedRecordByID(ctx, recordID, LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		t.Fatal(err)
	}
	if !record.Resolved() || record.ResolvedMediaID != reservedMediaID {
		t.Fatalf("finalized record = %#v, want resolution to %q", record, reservedMediaID)
	}
}

func TestUnmatchedCreateSerializesPreFinalizationStage(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	recordID, err := st.QueueUnmatchedRecord(ctx, UnmatchedRecord{
		Origin:        UnmatchedOriginScan,
		LibraryID:     "library-local",
		SourceHandle:  "/media/concurrent-artwork.mkv",
		FailureReason: UnmatchedReasonNoCandidates,
	})
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	type result struct {
		mediaID MediaID
		err     error
	}
	results := make(chan result, 2)
	create := func() {
		mediaID, err := st.CreateManualMediaFromUnmatched(
			ctx,
			recordID,
			"library-local",
			"Concurrent Artwork",
			2001,
			"digest-a",
			time.Unix(100, 0),
			func(MediaID) error {
				entered <- struct{}{}
				<-release
				return nil
			},
		)
		results <- result{mediaID: mediaID, err: err}
	}
	go create()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first pre-finalization stage did not start")
	}
	go create()
	select {
	case <-entered:
		t.Fatal("concurrent retry entered the pre-finalization stage")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	first := <-results
	second := <-results
	if first.err != nil || second.err != nil || first.mediaID != second.mediaID {
		t.Fatalf("serialized creates = %#v, %#v", first, second)
	}
}

// CreateManualMediaFixture is the shared hand-authored create the tests above
// retry and inspect.
func CreateManualMediaFixture(ctx context.Context, st *Store) (MediaID, error) {
	return st.CreateManualMedia(ctx, ManualMedia{
		LibraryID: "library-local",
		Path:      "/media/home-movie.mkv",
		Title:     "Home Movie 1994",
		Year:      1994,
	})
}

// TestManualMediaTitleSurvivesRescan is the "manual fields survive re-scan"
// guarantee the create-Media mockup's `created` state promises: the title is
// written at the identified tier, so a later scan of the same content carrying a
// filename-derived guess cannot overwrite what the operator typed.
func TestManualMediaTitleSurvivesRescan(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	filter := LibraryFilter{AllLibraries: true, Revealed: true}

	mediaID, err := CreateManualMediaFixture(ctx, st)
	if err != nil {
		t.Fatalf("CreateManualMedia: %v", err)
	}
	if _, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID:          "library-local",
		Path:               "/media/home-movie.mkv",
		Title:              "home.movie.1994.1080p",
		Container:          "mkv",
		Codec:              "h264",
		ContentFingerprint: manualFingerprint(mediaID),
	}); err != nil {
		t.Fatalf("rescan ImportMediaFile: %v", err)
	}
	media, err := st.Media(ctx, mediaID, filter)
	if err != nil {
		t.Fatalf("Media: %v", err)
	}
	if media.Title != "Home Movie 1994" {
		t.Fatalf("title after rescan = %q, want the operator's hand-entered title", media.Title)
	}
}
