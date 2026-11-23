package graph

import (
	"context"
	"testing"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func pinCandidateForTest(t *testing.T, handler *Handler, mediaID store.MediaID, slot, candidateID string) (ArtworkCandidateInfo, error) {
	t.Helper()
	return handler.pinArtworkCandidate(context.Background(), managerScope(), request{
		Variables: map[string]any{"mediaID": string(mediaID), "slot": slot, "candidateID": candidateID},
	})
}

func slotLocked(t *testing.T, st *store.Store, mediaID store.MediaID, slot string) bool {
	t.Helper()
	locks, err := st.MediaFieldLocks(context.Background(), mediaID, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, lock := range locks {
		if lock.Field == store.ArtworkLockField(slot) {
			return lock.Locked
		}
	}
	t.Fatalf("no lock field for slot %q in %#v", slot, locks)
	return false
}

// Every known slot is pinnable and lockable: a pin never changes the image
// and then fails on an unknown lock field (CORE-2905).
func TestPinArtworkCandidateLocksEveryKnownSlot(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	handler := newTestHandlerWithStore(st)
	handler.SetArtworkCandidateServices(&fakeArtworkCandidateFetcher{}, &fakeManualArtworkImporter{})
	for _, slot := range artwork.KnownSlots {
		sourceURL := "https://img.example.test/" + slot + ".jpg"
		if err := st.UpsertArtworkCandidate(ctx, "media-alpha", slot, sourceURL, "demo", "lookup"); err != nil {
			t.Fatal(err)
		}
		info, err := pinCandidateForTest(t, handler, "media-alpha", slot, store.ArtworkCandidateID("media-alpha", slot, sourceURL))
		if err != nil || !info.Applied {
			t.Fatalf("pin %s = %#v, %v", slot, info, err)
		}
		if !slotLocked(t, st, "media-alpha", slot) {
			t.Fatalf("slot %s not locked after pin", slot)
		}
	}
}

// A failed import leaves the catalog untouched: no lock, no applied flag.
// Re-pinning an uploaded candidate reads its cached bytes; the synthetic
// upload URL is never fetched, and a missing cache is a clear error.
type revertRecordingImporter struct {
	*fakeManualArtworkImporter
	reverted []string
}

func (r *revertRecordingImporter) RevertManualArtwork(_ context.Context, _ store.MediaID, slot string) error {
	r.reverted = append(r.reverted, slot)
	return nil
}

// Revert is not a plain unlock: it clears the slot's applied pick and gives
// the slot back, while an unlock keeps the picked image (CORE-2905).
func TestSetMediaFieldLockRevertGivesArtworkSlotBack(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	importer := &revertRecordingImporter{fakeManualArtworkImporter: &fakeManualArtworkImporter{}}
	handler := newTestHandlerWithStore(st)
	handler.SetArtworkCandidateServices(&fakeArtworkCandidateFetcher{}, importer)
	sourceURL := "https://img.example.test/poster.jpg"
	if err := st.UpsertArtworkCandidate(ctx, "media-alpha", artwork.SlotPoster, sourceURL, "demo", "lookup"); err != nil {
		t.Fatal(err)
	}
	candidateID := store.ArtworkCandidateID("media-alpha", artwork.SlotPoster, sourceURL)
	applied := func() bool {
		t.Helper()
		candidate, err := st.ArtworkCandidate(ctx, candidateID, store.LibraryFilter{Revealed: true, AllLibraries: true})
		if err != nil {
			t.Fatal(err)
		}
		return candidate.Applied
	}
	setLock := func(locked, revert bool) {
		t.Helper()
		vars := map[string]any{"mediaID": "media-alpha", "field": store.ArtworkLockField(artwork.SlotPoster), "locked": locked}
		if revert {
			vars["revert"] = true
		}
		if _, err := handler.setMediaFieldLock(ctx, managerScope(), request{Variables: vars}); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := pinCandidateForTest(t, handler, "media-alpha", artwork.SlotPoster, candidateID); err != nil {
		t.Fatal(err)
	}
	setLock(false, false)
	if !applied() || len(importer.reverted) != 0 {
		t.Fatalf("plain unlock: applied=%v reverted=%v; want pick kept", applied(), importer.reverted)
	}

	if _, err := pinCandidateForTest(t, handler, "media-alpha", artwork.SlotPoster, candidateID); err != nil {
		t.Fatal(err)
	}
	setLock(false, true)
	if applied() || slotLocked(t, st, "media-alpha", artwork.SlotPoster) {
		t.Fatalf("revert left the pick applied=%v or the slot locked", applied())
	}
	if len(importer.reverted) != 1 || importer.reverted[0] != artwork.SlotPoster {
		t.Fatalf("reverted = %v, want [poster]", importer.reverted)
	}

	// Revert on a non-artwork field is a plain unlock.
	vars := map[string]any{"mediaID": "media-alpha", "field": "title", "locked": false, "revert": true}
	if _, err := handler.setMediaFieldLock(ctx, managerScope(), request{Variables: vars}); err != nil {
		t.Fatal(err)
	}
	if len(importer.reverted) != 1 {
		t.Fatalf("reverted = %v, want no artwork revert for title", importer.reverted)
	}
}
