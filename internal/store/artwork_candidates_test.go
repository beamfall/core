package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestArtworkCandidatesUpsertIsIdempotentAndAdditive(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()

	if err := st.UpsertArtworkCandidate(ctx, "media-alpha", "poster", "https://images.example.test/a.jpg", "demo", "lookup"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertArtworkCandidate(ctx, "media-alpha", "poster", "https://images.example.test/a.jpg", "demo", "lookup"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertArtworkCandidate(ctx, "media-alpha", "poster", "https://images.example.test/b.jpg", "demo", "lookup"); err != nil {
		t.Fatal(err)
	}

	candidates, err := st.ArtworkCandidates(ctx, "media-alpha", "poster", LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(candidates), 2; got != want {
		t.Fatalf("len(candidates) = %d, want %d: %#v", got, want, candidates)
	}
	byURL := map[string]ArtworkCandidate{}
	for _, candidate := range candidates {
		byURL[candidate.SourceURL] = candidate
	}
	first := byURL["https://images.example.test/a.jpg"]
	if first.ID != ArtworkCandidateID("media-alpha", "poster", "https://images.example.test/a.jpg") {
		t.Fatalf("candidate id = %q, want deterministic hash", first.ID)
	}
	if first.PluginID != "demo" || first.ScraperID != "lookup" {
		t.Fatalf("provenance = %#v", first)
	}
}

func TestMarkArtworkCandidateAppliedIsExclusivePerMediaSlot(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	a := ArtworkCandidateID("media-alpha", "poster", "https://images.example.test/a.jpg")
	b := ArtworkCandidateID("media-alpha", "poster", "https://images.example.test/b.jpg")
	otherSlot := ArtworkCandidateID("media-alpha", "backdrop", "https://images.example.test/c.jpg")
	for _, row := range []struct {
		slot string
		url  string
	}{
		{"poster", "https://images.example.test/a.jpg"},
		{"poster", "https://images.example.test/b.jpg"},
		{"backdrop", "https://images.example.test/c.jpg"},
	} {
		if err := st.UpsertArtworkCandidate(ctx, "media-alpha", row.slot, row.url, "demo", "lookup"); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.MarkArtworkCandidateApplied(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkArtworkCandidateApplied(ctx, b); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkArtworkCandidateApplied(ctx, otherSlot); err != nil {
		t.Fatal(err)
	}

	posters, err := st.ArtworkCandidates(ctx, "media-alpha", "poster", LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	applied := map[string]bool{}
	for _, candidate := range posters {
		applied[candidate.ID] = candidate.Applied
	}
	if applied[a] || !applied[b] {
		t.Fatalf("poster applied flags = %#v, want only B applied", applied)
	}
	backdrops, err := st.ArtworkCandidates(ctx, "media-alpha", "backdrop", LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(backdrops) != 1 || !backdrops[0].Applied {
		t.Fatalf("backdrop applied flags = %#v, want untouched by poster pin", backdrops)
	}
}

func TestApplyArtworkCandidateRetryConverges(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	sourceURL := "beamfall-upload://sha256/content-digest"
	activity := ActivityEntry{
		OccurredAt: time.Unix(100, 0).UTC(),
		Action:     ActivityArtworkPin,
		ProfileID:  "profile-default",
		TargetType: "media",
		TargetID:   "media-alpha",
		LibraryID:  "library-local",
	}
	imports := 0
	rollbacks := 0
	for range 2 {
		if err := st.ApplyArtworkCandidate(ctx, ArtworkCandidateApply{
			MediaID:   "media-alpha",
			Slot:      "poster",
			SourceURL: sourceURL,
			Activity:  activity,
			ApplyArtwork: func() (func() error, error) {
				imports++
				return func() error {
					rollbacks++
					return nil
				}, nil
			},
		}); err != nil {
			t.Fatalf("ApplyArtworkCandidate: %v", err)
		}
	}
	if imports != 1 || rollbacks != 0 {
		t.Fatalf("imports=%d rollbacks=%d, want one import and no rollback", imports, rollbacks)
	}
	candidates, err := st.ArtworkCandidates(ctx, "media-alpha", "poster", LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || !candidates[0].Applied {
		t.Fatalf("candidates = %#v, want one applied candidate", candidates)
	}
	entries, err := st.ActivityEntries(ctx, ActivityQuery{Filter: LibraryFilter{Revealed: true, AllLibraries: true}})
	if err != nil {
		t.Fatal(err)
	}
	pins := 0
	for _, entry := range entries {
		if entry.Action == ActivityArtworkPin && entry.TargetID == "media-alpha" {
			pins++
		}
	}
	if pins != 1 {
		t.Fatalf("artwork pin activity rows = %d, want 1", pins)
	}
}

func TestApplyArtworkCandidateReapplyRecordsEachRealTransition(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	const (
		sourceA = "beamfall-upload://sha256/content-a"
		sourceB = "beamfall-upload://sha256/content-b"
	)
	imports := 0
	for _, sourceURL := range []string{sourceA, sourceB, sourceA} {
		if err := st.ApplyArtworkCandidate(ctx, ArtworkCandidateApply{
			MediaID:   "media-alpha",
			Slot:      "poster",
			SourceURL: sourceURL,
			Activity: ActivityEntry{
				OccurredAt: time.Unix(100, 0).UTC(),
				LibraryID:  "library-local",
			},
			ApplyArtwork: func() (func() error, error) {
				imports++
				return func() error { return nil }, nil
			},
		}); err != nil {
			t.Fatalf("ApplyArtworkCandidate(%q): %v", sourceURL, err)
		}
	}
	if imports != 3 {
		t.Fatalf("imports = %d, want 3 real state transitions", imports)
	}
	candidates, err := st.ArtworkCandidates(ctx, "media-alpha", "poster", LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	applied := map[string]bool{}
	for _, candidate := range candidates {
		applied[candidate.SourceURL] = candidate.Applied
	}
	if len(candidates) != 2 || !applied[sourceA] || applied[sourceB] {
		t.Fatalf("applied candidates = %#v, want only A applied", applied)
	}
	entries, err := st.ActivityEntries(ctx, ActivityQuery{Filter: LibraryFilter{Revealed: true, AllLibraries: true}})
	if err != nil {
		t.Fatal(err)
	}
	operationIDs := map[string]bool{}
	for _, entry := range entries {
		if entry.Action == ActivityArtworkPin && entry.TargetID == "media-alpha" {
			operationIDs[entry.OperationID] = true
		}
	}
	if len(operationIDs) != 3 || operationIDs[""] {
		t.Fatalf("artwork pin operation IDs = %#v, want 3 distinct non-empty IDs", operationIDs)
	}
}

func TestApplyArtworkCandidateImportFailureRollsBack(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	injected := errors.New("injected import failure")
	fileChanged := false
	err := st.ApplyArtworkCandidate(ctx, ArtworkCandidateApply{
		MediaID:   "media-alpha",
		Slot:      "poster",
		SourceURL: "beamfall-upload://sha256/failed-content",
		Activity: ActivityEntry{
			OccurredAt: time.Unix(100, 0).UTC(),
			Action:     ActivityArtworkPin,
			TargetType: "media",
			TargetID:   "media-alpha",
			LibraryID:  "library-local",
		},
		ApplyArtwork: func() (func() error, error) {
			fileChanged = true
			return func() error {
				fileChanged = false
				return nil
			}, injected
		},
	})
	if !errors.Is(err, injected) {
		t.Fatalf("ApplyArtworkCandidate error = %v, want injected failure", err)
	}
	if fileChanged {
		t.Fatal("artwork rollback did not restore the prior file state")
	}
	candidates, err := st.ArtworkCandidates(ctx, "media-alpha", "poster", LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Fatalf("candidates after failed apply = %#v, want none", candidates)
	}
	entries, err := st.ActivityEntries(ctx, ActivityQuery{Filter: LibraryFilter{Revealed: true, AllLibraries: true}})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Action == ActivityArtworkPin && entry.TargetID == "media-alpha" {
			t.Fatalf("activity after failed apply = %#v, want none", entry)
		}
	}
}

func TestApplyArtworkCandidateActivityFailureRestoresPriorState(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	priorURL := "https://images.example.test/prior.jpg"
	if err := st.UpsertArtworkCandidate(ctx, "media-alpha", "poster", priorURL, "demo", "lookup"); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkArtworkCandidateApplied(ctx, ArtworkCandidateID("media-alpha", "poster", priorURL)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.eventsW.ExecContext(ctx, `
		CREATE TEMP TRIGGER fail_artwork_pin_activity
		BEFORE INSERT ON activity_entry
		WHEN NEW.action = 'artwork.pin'
		BEGIN
		  SELECT RAISE(ABORT, 'injected artwork activity failure');
		END`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}
	fileState := "prior"
	err := st.ApplyArtworkCandidate(ctx, ArtworkCandidateApply{
		MediaID:   "media-alpha",
		Slot:      "poster",
		SourceURL: "beamfall-upload://sha256/new-content",
		Activity: ActivityEntry{
			OccurredAt: time.Unix(100, 0).UTC(),
			Action:     ActivityArtworkPin,
			TargetType: "media",
			TargetID:   "media-alpha",
			LibraryID:  "library-local",
		},
		ApplyArtwork: func() (func() error, error) {
			fileState = "new"
			return func() error {
				fileState = "prior"
				return nil
			}, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "injected artwork activity failure") {
		t.Fatalf("ApplyArtworkCandidate error = %v, want injected activity failure", err)
	}
	if fileState != "prior" {
		t.Fatalf("file state = %q, want prior", fileState)
	}
	candidates, err := st.ArtworkCandidates(ctx, "media-alpha", "poster", LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].SourceURL != priorURL || !candidates[0].Applied {
		t.Fatalf("candidates after activity failure = %#v, want only prior applied", candidates)
	}
}

func TestApplyArtworkCandidateCloseDuringImportRollsBack(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	importStarted := make(chan struct{})
	finishImport := make(chan struct{})
	applyResult := make(chan error, 1)
	fileState := "prior"
	go func() {
		applyResult <- st.ApplyArtworkCandidate(ctx, ArtworkCandidateApply{
			MediaID:   "media-alpha",
			Slot:      "poster",
			SourceURL: "beamfall-upload://sha256/close-race",
			Activity: ActivityEntry{
				OccurredAt: time.Unix(100, 0).UTC(),
				LibraryID:  "library-local",
			},
			ApplyArtwork: func() (func() error, error) {
				close(importStarted)
				<-finishImport
				fileState = "new"
				return func() error {
					fileState = "prior"
					return nil
				}, nil
			},
		})
	}()
	<-importStarted
	closeResult := make(chan error, 1)
	go func() { closeResult <- st.Close() }()
	select {
	case err := <-closeResult:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close blocked behind the artwork importer")
	}
	close(finishImport)
	select {
	case err := <-applyResult:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("ApplyArtworkCandidate error = %v, want ErrClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ApplyArtworkCandidate did not return after Close")
	}
	if fileState != "prior" {
		t.Fatalf("file state = %q, want prior", fileState)
	}
}

func TestArtworkCandidatesAreRevealFiltered(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	if err := st.UpsertArtworkCandidate(ctx, "media-archive", "poster", "https://images.example.test/hidden.jpg", "demo", "lookup"); err != nil {
		t.Fatal(err)
	}
	id := ArtworkCandidateID("media-archive", "poster", "https://images.example.test/hidden.jpg")

	hidden, err := st.ArtworkCandidates(ctx, "media-archive", "poster", LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(hidden) != 0 {
		t.Fatalf("hidden candidates = %#v, want empty", hidden)
	}
	if _, err := st.ArtworkCandidate(ctx, id, LibraryFilter{AllLibraries: true}); err == nil || err.Error() != "artwork candidate not found" {
		t.Fatalf("hidden candidate err = %v, want artwork candidate not found", err)
	}

	revealed, err := st.ArtworkCandidates(ctx, "media-archive", "poster", LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(revealed) != 1 || revealed[0].ID != id {
		t.Fatalf("revealed candidates = %#v, want hidden candidate", revealed)
	}
	if _, err := st.ArtworkCandidate(ctx, id, LibraryFilter{Revealed: true, AllLibraries: true}); err != nil {
		t.Fatalf("revealed candidate returned error: %v", err)
	}
}

func TestPurgeTrashedBeforeDeletesArtworkCandidates(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	revealed := LibraryFilter{AllowedIDs: []LibraryID{"library-local", "library-archive"}, Revealed: true}

	if err := st.UpsertArtworkCandidate(ctx, "media-alpha", "poster", "https://images.example.test/purged.jpg", "demo", "lookup"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertArtworkCandidate(ctx, "media-archive", "poster", "https://images.example.test/survivor.jpg", "demo", "lookup"); err != nil {
		t.Fatal(err)
	}
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
	if got := countCatalogRows(t, st, `SELECT COUNT(*) FROM artwork_candidate WHERE media_id = 'media-alpha'`); got != 0 {
		t.Fatalf("purged media candidates = %d, want 0", got)
	}
	if got := countCatalogRows(t, st, `SELECT COUNT(*) FROM artwork_candidate WHERE media_id = 'media-archive'`); got != 1 {
		t.Fatalf("surviving media candidates = %d, want 1", got)
	}
}
