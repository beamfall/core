package artwork

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/beamfall/core/internal/store/storetest"
)

// TestImportEntityArtworkSlots (CORE-2514): a container map's backdrop lands
// in the Entity's backdrop slot, the poster slot takes the entity poster path,
// a locally locked slot is refused and an unknown slot is an error.
func TestImportEntityArtworkSlots(t *testing.T) {
	ctx := context.Background()
	svc := NewService(storetest.Open(t), &fakeScaler{}, t.TempDir())

	if err := svc.ImportEntityArtwork(ctx, "entity-show", SlotBackdrop, []byte("backdrop-bytes")); err != nil {
		t.Fatalf("ImportEntityArtwork backdrop: %v", err)
	}
	if !svc.HasEntityArtwork("entity-show", SlotBackdrop) {
		t.Fatal("HasEntityArtwork(backdrop) = false after import")
	}
	if err := svc.ImportEntityArtwork(ctx, "entity-show", SlotPoster, []byte("poster-bytes")); err != nil {
		t.Fatalf("ImportEntityArtwork poster: %v", err)
	}
	if !svc.HasEntityPoster("entity-show") {
		t.Fatal("HasEntityPoster = false after a poster-slot import")
	}

	locked := svc.EntityArtworkPath("entity-locked", SlotBackdrop)
	if err := os.MkdirAll(filepath.Dir(locked), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := writeSlotLock(locked, slotLock{Source: SourceLocal, Path: "/library/fanart.jpg"}); err != nil {
		t.Fatal(err)
	}
	var lockedErr *SlotLockedError
	if err := svc.ImportEntityArtwork(ctx, "entity-locked", SlotBackdrop, []byte("scraped")); !errors.As(err, &lockedErr) {
		t.Fatalf("ImportEntityArtwork on a locked slot = %v, want *SlotLockedError", err)
	}
	if err := svc.ImportEntityArtwork(ctx, "entity-show", "nope", []byte("x")); err == nil {
		t.Fatal("ImportEntityArtwork accepted an unknown slot")
	}
}
