package artwork

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/beamfall/core/internal/store"
)

func writeSlotFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readSlotFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- a path under the test's temporary artwork root.
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatal(err)
	}
	return string(data)
}

// A title-fold merge (CORE-2459) gives the survivor every slot it has none of
// its own for, with its marks, and leaves the merged Entity's files in place
// so an undo restores it whole. The survivor's own artwork stays.
func TestAdoptEntityArtworkFillsOnlySlotsTheSurvivorLacks(t *testing.T) {
	s := newShelf(t)
	s.entity("entity-kept", "box")
	s.entity("entity-merged", "box")
	svc := s.svc
	kept, merged := store.EntityID("entity-kept"), store.EntityID("entity-merged")
	path := svc.EntityArtworkPath

	// The survivor has its own backdrop and an inherited poster; the merged
	// Entity has its own poster with a lock, a backdrop and a logo.
	writeSlotFile(t, path(kept, SlotBackdrop), "kept backdrop")
	writeSlotFile(t, path(kept, SlotPoster), "inherited poster")
	writeSlotFile(t, inheritedMarkerPath(path(kept, SlotPoster)), "{}")
	writeSlotFile(t, path(kept, SlotPoster)[:len(path(kept, SlotPoster))-len(".jpg")]+".w320.jpg", "stale bucket")
	writeSlotFile(t, path(merged, SlotPoster), "merged poster")
	writeSlotFile(t, lockMarkerPath(path(merged, SlotPoster)), `{"source":"local"}`)
	writeSlotFile(t, path(merged, SlotBackdrop), "merged backdrop")
	writeSlotFile(t, path(merged, SlotLogo), "merged logo")

	if err := svc.AdoptEntityArtwork(context.Background(), kept, merged); err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct{ path, body string }{
		{path(kept, SlotPoster), "merged poster"},
		{lockMarkerPath(path(kept, SlotPoster)), `{"source":"local"}`},
		{inheritedMarkerPath(path(kept, SlotPoster)), ""},
		{path(kept, SlotBackdrop), "kept backdrop"},
		{path(kept, SlotLogo), "merged logo"},
		{path(merged, SlotPoster), "merged poster"},
		{path(merged, SlotBackdrop), "merged backdrop"},
		{path(merged, SlotLogo), "merged logo"},
	} {
		if got := readSlotFile(t, want.path); got != want.body {
			t.Errorf("%s = %q, want %q", want.path, got, want.body)
		}
	}
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(path(kept, SlotPoster)), "poster.*.jpg"))
	if len(matches) != 0 {
		t.Errorf("stale poster buckets kept: %v", matches)
	}

	// The survivor's own poster is never replaced, and a second call changes
	// nothing.
	writeSlotFile(t, path(merged, SlotPoster), "newer merged poster")
	if err := svc.AdoptEntityArtwork(context.Background(), kept, merged); err != nil {
		t.Fatal(err)
	}
	if got := readSlotFile(t, path(kept, SlotPoster)); got != "merged poster" {
		t.Fatalf("own poster replaced: %q", got)
	}
}

// A survivor in a Sealed Library gets no artwork on the shared plane.
func TestAdoptEntityArtworkSkipsAProtectedSurvivor(t *testing.T) {
	s := newShelf(t)
	s.entity("entity-kept", "box")
	s.entity("entity-merged", "box")
	s.svc.SetSealed(func(store.LibraryID) bool { return true })
	writeSlotFile(t, s.svc.EntityArtworkPath("entity-merged", SlotPoster), "merged poster")
	if err := s.svc.AdoptEntityArtwork(context.Background(), "entity-kept", "entity-merged"); err != nil {
		t.Fatal(err)
	}
	if got := readSlotFile(t, s.svc.EntityArtworkPath("entity-kept", SlotPoster)); got != "" {
		t.Fatalf("sealed survivor got artwork %q", got)
	}
}
