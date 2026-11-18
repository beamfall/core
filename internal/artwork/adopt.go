package artwork

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/beamfall/core/internal/store"
)

// AdoptEntityArtwork copies the artwork of an Entity being merged into
// another to the survivor, slot by slot, wherever the survivor has none of
// its own: a slot it lacks, or a poster it only inherited while the merged
// Entity's is its own. Each copy carries the slot's lock and inherited marks.
// The merged Entity keeps its files, so undoing the merge restores it whole,
// and the survivor's own artwork is never replaced. A survivor in a Sealed or
// Encrypted Library gets nothing, as with every Entity artwork write.
func (s *Service) AdoptEntityArtwork(ctx context.Context, survivor, merged store.EntityID) error {
	if survivor == merged || s.entityProtected(ctx, survivor) {
		return nil
	}
	s.entityPosterMu.Lock()
	defer s.entityPosterMu.Unlock()
	unlock, err := s.lockDerivative(ctx, filepath.Dir(s.EntityPosterPath(survivor)))
	if err != nil {
		return err
	}
	defer unlock()
	for _, slot := range KnownSlots {
		src := storedSlotPath(s.EntityArtworkPath(merged, slot))
		if !nonEmptyRegularFile(src) {
			continue
		}
		own := storedSlotPath(s.EntityArtworkPath(survivor, slot))
		if regularFileExists(own) && (slot != SlotPoster || !regularFileExists(inheritedMarkerPath(own)) || regularFileExists(inheritedMarkerPath(src))) {
			continue
		}
		if err := s.copyAdoptedSlot(src, filepath.Join(filepath.Dir(own), filepath.Base(src)), slot); err != nil {
			return err
		}
	}
	return nil
}

// copyAdoptedSlot copies one slot file and its marks to dst. A mark src lacks
// is removed from dst, and dst's size buckets of the slot are dropped so they
// are made again from the copy.
func (s *Service) copyAdoptedSlot(src, dst, slot string) error {
	// #nosec G304 -- src is a slot path this service built under its artwork root.
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	defer s.versions.forget(dst)
	if err := writeAtomicFile(dst, data, nil); err != nil {
		return err
	}
	for _, mark := range []func(string) string{lockMarkerPath, inheritedMarkerPath, generatedMarkerPath} {
		// #nosec G304 -- the mark sits beside a slot path this service built.
		body, err := os.ReadFile(mark(src))
		switch {
		case err == nil:
			if err := writeAtomicFile(mark(dst), body, nil); err != nil {
				return err
			}
		case errors.Is(err, os.ErrNotExist):
			if err := os.Remove(mark(dst)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		default:
			return err
		}
	}
	for bucket := range bucketWidths {
		_ = os.Remove(filepath.Join(filepath.Dir(dst), slot+"."+bucket+filepath.Ext(dst)))
	}
	s.recordDominantColor(dst, slot)
	return nil
}
